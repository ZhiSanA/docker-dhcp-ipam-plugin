#!/bin/sh
# One-click test for DHCP IPAM plugin
# Runs the plugin standalone with sudo, creates a macvlan network,
# starts nginx, and shows the assigned IP.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_BIN="$SCRIPT_DIR/build/docker-dhcp-ipam-plugin"
SOCKET_PATH="/run/docker/plugins/dhcp_ipam.sock"
SPEC_PATH="/etc/docker/plugins/dhcp-ipam.spec"
NET_NAME="dhcp-net"
CONTAINER_NAME="dhcp-test-nginx"

echo "============================================"
echo "  DHCP IPAM Plugin - One-Click Test"
echo "============================================"

# Step 1: Build
echo ""
echo "[1/6] Building plugin..."
cd "$SCRIPT_DIR"
make build 2>&1 | tail -1
echo "  ✅ Build complete"

# Step 2: Kill old plugin + cleanup
echo ""
echo "[2/6] Cleaning up previous instances..."
pkill -f "docker-dhcp-ipam-plugin" 2>/dev/null || true
sleep 1
sudo rm -f "$SOCKET_PATH"
docker stop "$CONTAINER_NAME" 2>/dev/null || true
docker network rm "$NET_NAME" 2>/dev/null || true
echo "  ✅ Cleanup done"

# Step 3: Start plugin
echo ""
echo "[3/6] Starting plugin (sudo)..."
DHCP_IPAM_SOCKET_PATH="$SOCKET_PATH" \
  sudo setsid "$PLUGIN_BIN" &>/tmp/dhcp-plugin.log &
sleep 2
if [ -S "$SOCKET_PATH" ]; then
  echo "  ✅ Plugin socket ready at $SOCKET_PATH"
else
  echo "  ❌ Plugin failed to start. Check: cat /tmp/dhcp-plugin.log"
  exit 1
fi

# Step 4: Create spec file
echo ""
echo "[4/6] Creating Docker spec file..."
echo "unix://$SOCKET_PATH" | sudo tee "$SPEC_PATH" >/dev/null
echo "  ✅ Spec file created"

# Step 5: Create network
echo ""
echo "[5/6] Creating macvlan network..."
sleep 1
if docker network create -d macvlan --opt parent=wlan0 --ipam-driver dhcp-ipam "$NET_NAME" 2>&1; then
  echo "  ✅ Network '$NET_NAME' created"
else
  echo "  ❌ Network creation failed"
  echo "  Possible issues:"
  echo "    - Docker 29.x managed plugin bug (use standalone mode above)"
  echo "    - wlan0 not available (check: ip link show)"
  exit 1
fi

# Step 6: Start nginx
echo ""
echo "[6/6] Starting nginx..."
docker run -d --name "$CONTAINER_NAME" --network "$NET_NAME" nginx:alpine 2>&1
sleep 3

echo ""
echo "============================================"
echo "  Result"
echo "============================================"
docker inspect "$CONTAINER_NAME" --format \
  '  Container: {{.Name}}
  IP:        {{.NetworkSettings.Networks.dhcp-net.IPAddress}}
  MAC:       {{.NetworkSettings.Networks.dhcp-net.MacAddress}}
  Created:   {{.Created}}' 2>/dev/null || \
docker inspect "$CONTAINER_NAME" 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)[0]
n = d.get('NetworkSettings',{}).get('Networks',{})
for name, net in n.items():
    print(f'  Network: {name}')
    print(f'  IP:      {net.get(\"IPAddress\",\"?\")}')
    print(f'  MAC:     {net.get(\"MacAddress\",\"?\")}')
" 2>/dev/null

echo ""
echo "  Try: curl -s http://<IP> | head -5"
echo "  Logs: docker logs $CONTAINER_NAME"
echo ""

# Show plugin logs
echo "Plugin logs (last 3 lines):"
tail -3 /tmp/dhcp-plugin.log

echo ""
echo "============================================"
echo "  Test complete!"
echo "  Run ./cleanup-test.sh to tear down"
echo "============================================"
