#!/bin/sh
# Build and install the DHCP IPAM Docker plugin
# Usage: ./build.sh [plugin-name]
#        ./build.sh clean

set -e

NAME="${1:-ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin}"
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

# docker plugin push ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
# docker plugin install ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
# docker network create -d macvlan --subnet="10.0.0.0/15" --gateway="10.0.0.1" -o parent=ens18 --ipv6 --subnet="2409:8a50:a70:2110::/64" --gateway="2409:8a50:a70:2110::1" --ipam-driver ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest river
# docker plugin disable -f ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
# docker plugin upgrade ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
# docker plugin enable ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
