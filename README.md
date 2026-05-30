# Docker DHCP IPAM Plugin

A Docker IPAM driver plugin that allocates container IP addresses via DHCP from the host's LAN.

Designed for use with **macvlan** networks — containers receive IPs directly from the LAN's DHCP server, ensuring they are routable on the physical network without NAT.

## How It Works

```
Container → macvlan → DHCP IPAM plugin → DHCP request on LAN → DHCP server → IP lease
```

1. The plugin detects the host's network interface and subnet from the default route.
2. When Docker creates a macvlan network, the plugin validates the requested subnet matches the host's LAN.
3. When a container is attached, the plugin sends a DHCP `DISCOVER`/`REQUEST` on behalf of the container using a deterministic or user-specified MAC address.
4. The IP is returned to Docker with an ongoing renewal loop that keeps the lease alive.

## Prerequisites

- Docker CE with [managed plugin support](https://docs.docker.com/engine/extend/)
- Host connected to a LAN with a DHCP server
- `CAP_NET_RAW` and `CAP_NET_ADMIN` capabilities (set automatically by the plugin)

## Quick Start

### 1. Build and Install the Plugin

```bash
# Clone the repository
git clone https://github.com/tuzi/docker-dhcp-ipam-plugin.git
cd docker-dhcp-ipam-plugin

# Build binary → create rootfs → create & enable Docker managed plugin
# The plugin name defaults to fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin
./build.sh

# Or specify a custom name:
# ./build.sh my-registry/dhcp-ipam:latest
```

### 2. Create a macvlan Network

```bash
docker network create \
  --driver macvlan \
  --ipam-driver fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin:latest \
  --ipam-opt subnet=192.168.1.0/24 \
  --opt parent=eth0 \
  my-network
```

**Important:** The `subnet` specified in `--ipam-opt` must match the subnet detected on the host's default route interface. If omitted, the plugin will reject the request.

### 3. Run a Container

```bash
docker run --rm --network my-network --name my-app nginx:alpine
```

The container will receive an IP from the LAN's DHCP server.

### 4. Verify

```bash
docker inspect my-network
docker exec my-app ip addr show eth0
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DHCP_IPAM_INTERFACE` | (auto-detected) | Host interface to use for DHCP. Auto-detected from default route if empty. |
| `DHCP_IPAM_SOCKET_PATH` | `/run/docker/plugins/dhcp-ipam.sock` | Unix socket path for Docker plugin communication. |
| `DHCP_IPAM_TIMEOUT` | `10s` | DHCP request timeout (Go duration format, e.g. `5s`, `30s`). |
| `DHCP_IPAM_RETRIES` | `3` | Number of DHCP request retries on failure. |
| `DHCP_IPAM_RENEW_INTERVAL` | `30s` | Interval for checking lease renewal status. |
| `DHCP_IPAM_MAC_FROM_NAME` | `true` | When enabled, generates a deterministic MAC from the container name (`com.docker.network.endpoint.name`). This ensures rebuilding a container with the same name gets the same IP. Set to `false` to use Docker-assigned MAC addresses instead. |

### Set environment variables on plugin creation

```bash
# Set a custom interface
docker plugin set fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin DHCP_IPAM_INTERFACE=eth1
```

Or set them at build time by editing `config.json` and adding to the `"env"` array:

```json
"env": [
  {"name": "DHCP_IPAM_INTERFACE", "value": "eth0"},
  {"name": "DHCP_IPAM_TIMEOUT", "value": "15s"}
]
```

## How MAC Addresses Are Determined

The plugin supports three MAC resolution strategies, checked in this order:

1. **MAC from container name** (default, `DHCP_IPAM_MAC_FROM_NAME=true`) — A deterministic MAC is derived from the container name via FNV-32a hash. Since the name stays the same across `docker compose up/down`, the container receives the same DHCP lease every time.
2. **MAC from Docker metadata** — Falls back to the MAC address Docker assigns to the container endpoint (`com.docker.network.endpoint.macaddress`).
3. **Fallback hash** — Generates a MAC from the pool ID (CIDR) as last resort.

## Files

```
├── main.go              # Entry point
├── internal/
│   ├── config.go        # Config struct, env var loading
│   ├── driver.go        # IPAM driver (RequestPool, RequestAddress, ReleasePool, ReleaseAddress)
│   ├── dhcp.go          # DHCP client wrapper (Obtain, Renew, Release, RenewLoop)
│   ├── interface.go     # Host interface detection (/proc/net/route, net.Interface)
│   ├── store.go         # In-memory PoolStore and LeaseStore
│   └── mac.go           # MAC resolution and generation
├── config.json          # Docker managed plugin manifest
├── build.sh             # Build script for Docker managed plugin
├── docker-compose.yaml  # Test compose file
└── go.mod / go.sum      # Go module dependencies
```

## Troubleshooting

### Check plugin logs

```bash
journalctl -fu docker | grep dhcp-ipam
```

### "pool must be specified"

The `subnet` option is required when creating the network. Example:

```bash
docker network create ... --ipam-opt subnet=192.168.1.0/24
```

### "requested pool X != detected subnet Y"

The subnet specified in `--ipam-opt subnet=...` does not match the host's detected subnet. Verify the host interface and its subnet:

```bash
ip route show default
ip addr show <interface>
```

### "static address not supported"

The plugin only allocates addresses via DHCP. Static IP assignments are not supported for individual containers (the gateway is handled automatically by Docker).

### Containers get the same IP

Ensure `DHCP_IPAM_MAC_FROM_NAME=true` (the default) and containers have distinct names. If containers share the same name, they will derive the same MAC and receive the same lease.

### Plugin fails to load

Make sure the plugin has the required capabilities:

```bash
docker plugin inspect <plugin-name>
```

Look for `Capabilities` including `CAP_NET_RAW` and `CAP_NET_ADMIN`.

## Building from Source

```bash
# Build standalone binary
go build -o docker-dhcp-ipam-plugin .

# Build Docker managed plugin
./build.sh
```

## License

MIT