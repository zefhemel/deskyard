#!/bin/sh
# Run as the ordinary desktop user. Only this script's unique tag is removed.
set -eu
cd "$(dirname "$0")"
tag="deskyard-docker-demo:$(cat /proc/sys/kernel/random/uuid)"
cleanup() { docker image rm "$tag" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker version --format 'Docker client {{.Client.Version}} / server {{.Server.Version}}'
docker build --progress=plain --label deskyard.example=docker -t "$tag" .
output=$(docker run --rm --label deskyard.example=docker "$tag")
printf '%s\n' "$output"
test "$output" = 'Hello from an image built inside a Deskyard desktop!'
