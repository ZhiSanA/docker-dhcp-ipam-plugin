#!/bin/sh
# Build and install the DHCP IPAM Docker plugin
# Usage: ./build.sh [plugin-name]
#        ./build.sh clean

set -e

NAME="${1:-tuzi/dhcp-ipam}"
BINARY="docker-dhcp-ipam-plugin"
PLUGIN_DIR="./plugin"

clean() {
	rm -rf build "$PLUGIN_DIR"
	docker plugin rm "$NAME" 2>/dev/null || true
	docker rmi "$NAME-rootfs" 2>/dev/null || true
	echo "✅ Clean"
}

if [ "$1" = "clean" ]; then
	clean
	exit 0
fi

echo "==> Building binary..."
CGO_ENABLED=0 go build -ldflags="-s -w" -o build/"$BINARY" .
echo "    ✅ build/$BINARY"

echo "==> Building plugin: $NAME"
mkdir -p "$PLUGIN_DIR/rootfs"
cp build/"$BINARY" "$PLUGIN_DIR/rootfs/"
cp config.json "$PLUGIN_DIR/"
docker build -t "$NAME-rootfs" .
docker create --name tmp-"$BINARY" "$NAME-rootfs"
docker export tmp-"$BINARY" | tar x -C "$PLUGIN_DIR/rootfs/"
docker rm -vf tmp-"$BINARY"
docker plugin rm "$NAME" 2>/dev/null || true
docker plugin create "$NAME" "$PLUGIN_DIR/"
docker plugin enable "$NAME"
echo "✅ Plugin $NAME created and enabled"
