#!/bin/bash
set -e

PROXY_HOST=${PROXY_HOST:-localhost}
PROXY_PORT=${PROXY_PORT:-443}
PROXY_PORT_HTTP=${PROXY_PORT_HTTP:-80}
AUTOCERT=${AUTOCERT:-false}
DEBUG=${DEBUG:-false}

[ -z "$API" ] ||
	echo "API=$API" >>"$ENV_FILE"
[ -z "$APP" ] ||
	echo "APP=$APP" >>"$ENV_FILE"
[ -z "$HOMEDEST" ] ||
	echo "HOMEDEST=$HOMEDEST" >>"$ENV_FILE"
[ -z "$AUTH_PATH" ] ||
	echo "AUTH_PATH=$AUTH_PATH" >>"$ENV_FILE"

mkdir -p "$DOCKER_BINDS_DIR"/certificates

getip() {
	if result=$(getent ahostsv4 "$1" 2>/dev/null); then
		echo "$result" | awk '{ print $1 ; exit }'
	elif result=$(ping -4 -n 1 "$1" 2>/dev/null); then
		echo "$result" | grep "$1" | sed 's~.*\[\(.*\)\].*~\1~'
		# Pinging wouter [10.0.75.1] with 32 bytes of data:
	elif result=$(ping -c 1 "${1}" 2>/dev/null); then
		echo "$result" | grep PING | grep -o -E '\d+\.\d+\.\d+\.\d+'
		# PING macbook-pro-van-wouter.local (188.166.80.233): 56 data bytes
	else
		echo '127.0.0.1'
	fi
}

urlhost() {
	echo "$1" | sed 's~.*//\([^:/]*\).*~\1~'
}

PROXY_PORT=$(docker4gis/port.sh "$PROXY_PORT")
PROXY_PORT_HTTP=$(docker4gis/port.sh "$PROXY_PORT_HTTP")

docker container run --restart "$RESTART" --name "$CONTAINER" \
	--env-file "$ENV_FILE" \
	--env PROXY_HOST="$PROXY_HOST" \
	--env PROXY_PORT="$PROXY_PORT" \
	--env AUTOCERT="$AUTOCERT" \
	--mount type=bind,source="$DOCKER_BINDS_DIR"/certificates,target=/certificates \
	--mount source="$VOLUME",target=/config \
	--network "$NETWORK" \
	--publish "$PROXY_PORT":443 \
	--publish "$PROXY_PORT_HTTP":80 \
	--add-host="$(hostname)":"$(getip "$(hostname)")" \
	--detach "$IMAGE" proxy "$@"

# Loop over the config files in the proxy volume, and connect the proxy
# container to any docker network of that name, so that the one proxy container
# can reach different applications' components' containers.
for network in $(docker container exec "$CONTAINER" ls /config); do
	if docker network inspect "$network" >/dev/null 2>&1; then
		docker network connect "$network" "$CONTAINER"
	fi
done
