package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
)

var (
	cookieMap = make(map[string]*Set)
)

func logRequest(r *http.Request) {
	curl := fmt.Sprintf("curl '%s' \\\n", r.URL)
	curl += fmt.Sprintf("  --request %s \\\n", r.Method)
	for header, values := range r.Header {
		curl += fmt.Sprintf("  -H '%s: ", header)
		for i, value := range values {
			if i > 0 {
				curl += "; "
			}
			curl += value
		}
		curl += "' \\\n"
	}
	if r.Method == "PUT" || r.Method == "POST" {
		curl += "  --data-raw '__TODO__paste_the_request_body_here' \\\n"
	}
	curl += "  --insecure \\\n"
	curl += "  --include"
	log.Printf("%s", curl)
}

type CustomTransport struct {
	BaseTransport http.RoundTripper
}

// Use a custom RoundTrip method to short-circuit certain requests: respond to
// them directly, without forwarding them to the designated target.
func (t *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	shortCircuitStatusCode := req.Header.Get("X-Short-Circuit-Status-Code")

	if shortCircuitStatusCode != "" {
		shortCircuitMessage := req.Header.Get("X-Short-Circuit-Message")

		// Parse to integer.
		statusCode, err := strconv.Atoi(shortCircuitStatusCode)
		if err != nil {
			statusCode = http.StatusInternalServerError
			shortCircuitMessage = "Invalid X-Short-Circuit-Status-Code"
		}

		resp := &http.Response{
			StatusCode: statusCode,
			Status:     http.StatusText(statusCode),
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}

		resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp.Header.Set("X-Short-Circuit-Message", shortCircuitMessage)

		log.Printf("%s - %s", shortCircuitMessage, req.URL)

		return resp, nil
	}

	// Otherwise, forward the request as normal, using the original transport.
	return t.BaseTransport.RoundTrip(req)
}

func reverseProxy(r *http.Request, path, app, key string,
	config *config, proxy *proxy) *httputil.ReverseProxy {

	keyWithoutTrailingSlash := strings.TrimSuffix(key, "/")

	// Prepend the app name to a path (either "" or starting with "/"), if the
	// app name was included in the client's request.
	appPath := func(path string) string {
		if r.URL.Path == "/"+app || strings.HasPrefix(r.URL.Path, "/"+app+"/") {
			path = "/" + app + path
		}
		return path
	}

	var cachePathHeader http.Header

	director := func(r *http.Request) {

		decorate(r, key)

		if proxy.authorise {
			// Have this request authorised at AUTH_PATH.
			if err := authorise(r, path, config.authPath); err != nil {
				return
			}
		}

		if proxy.cache {
			// Check cache status for this request at CACHE_PATH.
			header, err := cache(r, path, config.cachePath)
			cachePathHeader = header
			if err != nil {
				return
			}
		}

		realIp := r.Header.Get("X-Real-Ip")
		if realIp == "" {
			realIp, _, _ = net.SplitHostPort(r.RemoteAddr)
			r.Header.Set("X-Real-Ip", realIp)
		}

		forwardedHost := r.Host
		r.Header.Set("X-Forwarded-Host", forwardedHost)

		forwardedProto := "https" // r.URL.Scheme == ""
		r.Header.Set("X-Forwarded-Proto", forwardedProto)

		forwardedPath := appPath(keyWithoutTrailingSlash)
		r.Header.Set("X-Forwarded-Path", forwardedPath)
		r.Header.Set("X-Forwarded-Prefix", forwardedPath)
		r.Header.Set("X-Script-Name", forwardedPath)

		forwarded := fmt.Sprintf("for=%s;host=%s;proto=%s;path=%s",
			realIp, forwardedHost, forwardedProto, forwardedPath)
		r.Header.Set("Forwarded", forwarded)

		target := proxy.target
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		r.URL.Path = target.Path + strings.SplitN(path, "/", 3)[2] // alles na de tweede slash
		if proxy.impersonate {
			r.Host = proxyHost
			if target.Port() != "" {
				r.Host += ":" + target.Port()
			}
		} else {
			r.Host = target.Host
		}
		if _, ok := r.Header["User-Agent"]; !ok {
			// explicitly disable User-Agent so it's not set to default value
			r.Header.Set("User-Agent", "")
		}

		// Prevent forwarding other destinations' cookies (specifically: don't
		// forward internal authentication cookies, that are set with a Path of
		// "/" or "/{app}", to support the AUTH_PATH functionality). Using the
		// cookieMap to match a cookie with the proxy path that set it (note
		// that cookies from a Request do not carry a Path value, like cookies
		// from a Response do).
		clonedRequest := r.Clone(r.Context())
		r.Header.Del("Cookie")
		// Repopulate with just those cookies from the request that were
		// previously jarred for this key.
		if names, ok := cookieMap[key]; ok {
			for _, name := range names.List() {
				if cookie, err := clonedRequest.Cookie(name); err == nil {
					r.AddCookie(cookie)
				}
			}
		}

		if debug && false {
			logRequest(r)
		}

		cookieNames := make([]string, 0)
		for _, cookie := range r.Cookies() {
			cookieNames = append(cookieNames, cookie.Name)
		}
		log.Printf("%s %s Reverse %v %s %s",
			r.RemoteAddr, r.Method, r.Host, r.URL, cookieNames)
	}

	modifyResponse := func(resp *http.Response) error {

		cors(resp.Header, r)

		for key, values := range cachePathHeader {
			for _, value := range values {
				resp.Header.Add(key, value)
			}
		}

		// Rewrite all cookies' Domain and Path to match the proxy client's
		// perspective.
		cookieSet := NewSet()
		if set, ok := cookieMap[key]; ok {
			cookieSet = set
		} else {
			cookieMap[key] = cookieSet
		}
		cookies := resp.Cookies()
		resp.Header.Del("Set-Cookie")
		for _, cookie := range cookies {

			// Put this cookie in the cookieMap (it's used in the Director to
			// filter cookies on the request's URL).
			cookieSet.Add(cookie.Name)

			// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Set-Cookie#domaindomain-value
			cookie.Domain = ""

			oldCookiePath := cookie.Path

			if strings.HasPrefix(config.authPath, proxy.target.String()) &&
				(cookie.Path == "" ||
					cookie.Path == "/" ||
					cookie.Path == appPath(keyWithoutTrailingSlash) ||
					cookie.Path == appPath(key)) {
				// This cookie is probably an authentication token; map it to
				// the root of the application, so that it will be sent with
				// requests for other keys (i.e. proxied paths), to render these
				// requests authenticated when put through AUTH_PATH.
				cookie.Path = appPath("/")
			} else if !(cookie.Path == appPath(keyWithoutTrailingSlash) ||
				strings.HasPrefix(cookie.Path, appPath(key))) {
				// Map the remote site's root to our proxy key.
				if !(cookie.Path == keyWithoutTrailingSlash ||
					strings.HasPrefix(cookie.Path, key)) {
					cookie.Path = keyWithoutTrailingSlash + cookie.Path
				}
				cookie.Path = appPath(cookie.Path)
			}

			if debug {
				log.Printf("-- %s cookie %s: path %s -> %s",
					r.URL, cookie.Name, oldCookiePath, cookie.Path)
			}

			// Put the rewritten cookie back in the response's header.
			resp.Header.Add("Set-Cookie", cookie.String())
		}

		return nil
	}

	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy.insecure {
		baseTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	transport := &CustomTransport{BaseTransport: baseTransport}

	return &httputil.ReverseProxy{
		Director:       director,
		ModifyResponse: modifyResponse,
		Transport:      transport,
	}
}

func decorate(r *http.Request, key string) {
	if key == "/geoserver/" {
		accessToken(r)
		envelope(r)
	}
}

func accessToken(r *http.Request) {
	access_token := getEnv(r, "access_token")
	if access_token != "" {
		// Deprecated:
		addViewparam(r, "access_token", access_token)
		return
	}
	access_token = getViewparam(r, "access_token")
	if access_token != "" {
		addEnv(r, "access_token", access_token)
		return
	}
	if username, password, ok := r.BasicAuth(); ok && username == "access_token" {
		r.Header.Del("Authorization")
		addEnv(r, username, password)
		// Deprecated:
		addViewparam(r, username, password)
	}
}

func envelope(r *http.Request) {
	_, bbox := getQuery(r, "bbox")
	if bbox == "" {
		return
	}

	_, crs := getQuery(r, "crs")
	_, srs := getQuery(r, "srs")
	if crs == "" && srs == "" {
		return
	}
	if srs == "" {
		srs = crs
	}

	// Split srs on :, and assign the last part to it.
	parts := strings.Split(srs, ":")
	if len(parts) > 1 {
		srs = parts[len(parts)-1]
	}

	addEnv(r, "envelope", bbox+","+srs)
}

func getEnv(r *http.Request, key string) (value string) {
	_, env := getQuery(r, "env")
	return getKV(env, key)
}

func addEnv(r *http.Request, key string, value string) {
	env_key, env_value := getQuery(r, "env")
	env_value = addKV(env_value, key, value)
	setQuery(r, env_key, env_value)
}

func getViewparam(r *http.Request, key string) (value string) {
	_, viewparams := getQuery(r, "viewparams")
	escapedComma := "||EscapedComma||"
	// Replace "\," with escapedComma in viewparams.
	viewparams = strings.ReplaceAll(viewparams, "\\,", escapedComma)
	// Split viewparams on ",", and assign the resulting slice to parts.
	parts := strings.Split(viewparams, ",")
	for _, layerParams := range parts {
		// Replace escapedComma with "," in layerParams.
		layerParams = strings.ReplaceAll(layerParams, escapedComma, ",")
		value = getKV(layerParams, key)
		if value != "" {
			break
		}
	}
	return value
}

func addViewparam(r *http.Request, key string, value string) {
	viewparams_key, viewparams := getQuery(r, "viewparams")
	new_viewparams := ""
	escapedComma := "||EscapedComma||"
	// Replace "\," with escapedComma in viewparams.
	viewparams = strings.ReplaceAll(viewparams, "\\,", escapedComma)
	// Split viewparams on ",", and assign the resulting slice to parts.
	parts := strings.Split(viewparams, ",")
	for i, layerParams := range parts {
		// Replace escapedComma with "," in layerParams.
		layerParams = strings.ReplaceAll(layerParams, escapedComma, ",")
		layerParams = addKV(layerParams, key, value)
		if i > 0 {
			new_viewparams += ","
		}
		new_viewparams += layerParams
	}
	setQuery(r, viewparams_key, new_viewparams)
}

func getKV(source string, key string) (value string) {
	escapedSemicolon := "||EscapedSemicolon||"
	// Replace "\;" with escapedSemicolon in source.
	source = strings.ReplaceAll(source, "\\;", escapedSemicolon)
	// Split source on ";", and assign the resulting slice to parts.
	parts := strings.Split(source, ";")
	for _, part := range parts {
		// Replace escapedSemicolon with ";" in part.
		part = strings.ReplaceAll(part, escapedSemicolon, ";")
		escapedColon := "||EscapedColon||"
		// Replace "\:" with escapedColon in part.
		part = strings.ReplaceAll(part, "\\:", escapedColon)
		// Split part on ":", and assign the resulting slice to kv.
		kv := strings.Split(part, ":")
		if len(kv) == 2 {
			if strings.ReplaceAll(kv[0], escapedColon, ":") == key {
				value = strings.ReplaceAll(kv[1], escapedColon, ":")
				break
			}
		}
	}
	return value
}

func addKV(source string, key string, value string) (result string) {
	key = strings.ReplaceAll(key, ":", "\\:")
	key = strings.ReplaceAll(key, ";", "\\;")
	value = strings.ReplaceAll(value, ":", "\\:")
	value = strings.ReplaceAll(value, ";", "\\;")
	result = source
	if len(result) > 0 {
		result += ";"
	}
	return result + key + ":" + value
}

func getQuery(r *http.Request, uncasedKey string) (key string, value string) {
	query := r.URL.Query()

	key = strings.ToLower(uncasedKey)
	value = query.Get(key)
	if value == "" {
		key = strings.ToUpper(key)
		value = query.Get(key)
	}
	return key, value
}

func setQuery(r *http.Request, key string, value string) {
	query := r.URL.Query()
	query.Set(key, value)
	r.URL.RawQuery = query.Encode()
}
