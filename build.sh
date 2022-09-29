#!/bin/bash

IMAGE=${IMAGE:-docker4gis/proxy}
DOCKER_BASE=$(npx --yes docker4gis@"${DOCKER4GIS_VERSION:-latest}" base)
DOCKER_USER=$DOCKER_USER

mkdir -p conf
cp -r "$DOCKER_BASE"/.plugins "$DOCKER_BASE"/.docker4gis conf
docker image build \
	--build-arg DOCKER_USER="$DOCKER_USER" \
	-t "$IMAGE" .
rm -rf conf/.plugins conf/.docker4gis
