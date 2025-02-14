FROM golang:1.23.1 AS builder
COPY goproxy /goproxy
WORKDIR /goproxy
RUN CGO_ENABLED=0 GOOS=linux go build -v -a -tags netgo -ldflags -w .

FROM alpine:3.12

RUN apk update
RUN apk add --no-cache \
    ca-certificates wget bash

COPY conf/certificates /tmp/conf/certificates
COPY conf/entrypoint /entrypoint
COPY conf/conf.sh /conf.sh

EXPOSE 80 443 8080

COPY --from=builder /goproxy/proxy /

# Example plugin use.
COPY conf/.plugins/bats /tmp/bats
RUN /tmp/bats/install.sh

# Allow configuration before things start up.
COPY conf/entrypoint /
ENTRYPOINT ["/entrypoint"]
CMD ["proxy"]

# Make this image work with dg build & dg push.
COPY conf/.docker4gis /.docker4gis
COPY build.sh run.sh /.docker4gis/

# Set environment variables.
ONBUILD ARG DOCKER_REGISTRY
ONBUILD ENV DOCKER_REGISTRY=$DOCKER_REGISTRY
ONBUILD ARG DOCKER_USER
ONBUILD ENV DOCKER_USER=$DOCKER_USER
ONBUILD ARG DOCKER_REPO
ONBUILD ENV DOCKER_REPO=$DOCKER_REPO

# Make this an extensible base component; see
# https://github.com/merkatorgis/docker4gis/tree/npm-package/docs#extending-base-components.
COPY template /template/

ONBUILD COPY conf/args /.docker4gis/
