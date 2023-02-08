#!/bin/bash
set -e

PROXY_HOST=${PROXY_HOST:-localhost}
PROXY_PORT=${PROXY_PORT:-443}
PROXY_PORT_HTTP=${PROXY_PORT_HTTP:-80}
AUTOCERT=${AUTOCERT:-false}
DEBUG=${DEBUG:-false}

SECRET=$SECRET
API=$API
AUTH_PATH=$AUTH_PATH
APP=$APP
HOMEDEST=$HOMEDEST

IMAGE=$IMAGE
CONTAINER=$CONTAINER
DOCKER_ENV=$DOCKER_ENV
RESTART=$RESTART
NETWORK=$NETWORK
FILEPORT=$FILEPORT
VOLUME=$VOLUME
DOCKER_BINDS_DIR=$DOCKER_BINDS_DIR

mkdir -p "$DOCKER_BINDS_DIR"/certificates

NETWORK=$CONTAINER

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
	-e PROXY_HOST="$PROXY_HOST" \
	-e PROXY_PORT="$PROXY_PORT" \
	-e AUTOCERT="$AUTOCERT" \
	-e DOCKER_ENV="$DOCKER_ENV" \
	-e DEBUG="$DEBUG" \
	-e "$(docker4gis/noop.sh SECRET "$SECRET")" \
	-e "$(docker4gis/noop.sh API "$API")" \
	-e "$(docker4gis/noop.sh AUTH_PATH "$AUTH_PATH")" \
	-e "$(docker4gis/noop.sh APP "$APP")" \
	-e "$(docker4gis/noop.sh HOMEDEST "$HOMEDEST")" \
	-v "$(docker4gis/bind.sh "$DOCKER_BINDS_DIR"/certificates /certificates)" \
	-p "$PROXY_PORT":443 \
	-p "$PROXY_PORT_HTTP":80 \
	--add-host="$(hostname)":"$(getip "$(hostname)")" \
	-e DOCKER_ENV="$DOCKER_ENV" \
	--mount source="$VOLUME",target=/config \
	--network "$NETWORK" \
	-d "$IMAGE" proxy "$@"

# Loop over the config files in the proxy volume, and connect the proxy
# container to any docker network of that name, so that the one proxy container
# can reach different applications' components' containers.
for network in $(docker container exec "$CONTAINER" ls /config); do
	if docker network inspect "$network" 1>/dev/null 2>&1; then
		docker network connect "$network" "$CONTAINER"
	fi
done
