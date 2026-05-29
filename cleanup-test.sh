#!/bin/sh
# Tear down the test environment
set -e

echo "Cleaning up..."
docker stop dhcp-test-nginx 2>/dev/null || true
docker network rm dhcp-net 2>/dev/null || true
pkill -f "docker-dhcp-ipam-plugin" 2>/dev/null || true
sudo rm -f /run/docker/plugins/dhcp_ipam.sock
sudo rm -f /etc/docker/plugins/dhcp-ipam.spec
echo "✅ Done"
