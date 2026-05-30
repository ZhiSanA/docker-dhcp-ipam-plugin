#!/bin/sh
# Build and install the DHCP IPAM Docker plugin
# Usage: ./build.sh [plugin-name]
#        ./build.sh clean

set -e

NAME="${1:-fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin}"
BINARY="docker-dhcp-ipam-plugin"

echo "==> Cleaning up old plugin..."
docker plugin rm --force "$NAME" 2>/dev/null || true
echo "    ✅ Done"

WORKDIR=$(mktemp -d)
mkdir -p "$WORKDIR/rootfs"

echo "==> Building binary..."
CGO_ENABLED=0 GOARCH=amd64 go build -ldflags="-s -w" -o "$WORKDIR/rootfs/$BINARY" .
echo "    ✅ Built $BINARY"

echo "==> Creating plugin: $NAME"
cp config.json "$WORKDIR/"
docker plugin create "$NAME" "$WORKDIR/"
docker plugin enable "$NAME"
echo "✅ Plugin $NAME created and enabled"

rm -rf "$WORKDIR"
