# Docker DHCP IPAM Plugin

A Docker IPAM (IP Address Management) driver plugin that allocates container IP addresses via DHCP from the host's LAN. Each container gets a real, routable LAN IP address through a full DHCP DORA cycle.

## How It Works

When a container is created on a network using this IPAM driver:

1. A unique MAC address is generated for the container (`02:1a:2b:xx:yy:zz`, locally administered)
2. A DHCP Discover-Offer-Request-Ack (DORA) cycle is performed on the host's physical interface
3. The leased IP address is returned to Docker and assigned to the container
4. A background goroutine handles lease renewal (at ~80% of T1/lease time)
5. When the container is removed, DHCPRELEASE is sent back to the DHCP server

## Prerequisites

- Docker CE/EE (tested on Docker 29.x)
- Host connected to a LAN with a DHCP server
- Linux (requires `CAP_NET_RAW` for raw DHCP sockets)

## Build & Install

### From source

```bash
# Build the plugin binary
make build

# Package and install as Docker managed plugin
make plugin
```

### Manual plugin setup

```bash
# Build binary
CGO_ENABLED=0 go build -ldflags="-s -w" -o build/docker-dhcp-ipam-plugin ./cmd/docker-dhcp-ipam-plugin

# Create rootfs
mkdir -p plugin-rootfs
docker build -t dhcp-ipam-rootfs .
docker create --name dhcp-ipam-extract dhcp-ipam-rootfs
docker export dhcp-ipam-extract | tar x -C plugin-rootfs/
docker rm dhcp-ipam-extract
cp config.json plugin-rootfs/

# Create and enable plugin
docker plugin create dhcp-ipam plugin-rootfs/
docker plugin enable dhcp-ipam
```

## Usage

Create a macvlan network using the DHCP IPAM driver:

```bash
docker network create \
  --driver macvlan \
  --opt parent=eth0 \
  --ipam-driver dhcp-ipam \
  dhcp-net
```

Run a container on this network:

```bash
docker run --network dhcp-net --rm alpine ip addr
```

The container will receive an IP address from the LAN's DHCP server.

## Configuration

Configuration is via environment variables passed to the plugin:

| Variable | Default | Description |
|---|---|---|
| `DHCP_IPAM_INTERFACE` | (auto-detect) | Network interface for DHCP (e.g., `eth0`, `wlan0`) |
| `DHCP_IPAM_SOCKET_PATH` | `/run/docker/plugins/dhcp_ipam.sock` | Unix socket path |
| `DHCP_IPAM_LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `DHCP_IPAM_TIMEOUT` | `10s` | DHCP request timeout |
| `DHCP_IPAM_RETRIES` | `3` | DHCP request retry count |

```bash
docker plugin set dhcp-ipam DHCP_IPAM_INTERFACE=eth1
docker plugin set dhcp-ipam DHCP_IPAM_LOG_LEVEL=debug
```

## Architecture

```
Docker daemon → Unix socket → IPAM handler → IPAM driver → DHCP client → LAN DHCP server
```

- **cmd/main.go** — HTTP server entry point with custom handler (lowercase manifest for Docker 29.x compat)
- **pkg/ipam** — Core driver implementing 6 IPAM API methods (GetCapabilities, RequestPool, RequestAddress, etc.)
- **pkg/dhcp** — DHCP client wrapper around `nclient4` (DORA, Renew, Release, auto-renewal)
- **pkg/iface** — Auto-detects host interface, subnet, gateway via `/proc/net/route`
- **pkg/store** — Thread-safe in-memory pool and lease stores
- **pkg/config** — Environment variable configuration

## Testing

```bash
make test          # Run all tests with race detection
go test -v ./pkg/iface/...  # Test interface detection
go test -v ./pkg/store/...  # Test store implementations
```

## Known Issues

- **Docker 29.x managed plugin bug**: `docker plugin create` stores interface types with a leading `.` and trailing `/` prefix/suffix, causing `"ipamdriver"` capability matching to fail. The Plugin.Activate endpoint returns the correct manifest. A workaround is being investigated.
- Requires `CAP_NET_RAW` for raw DHCP sockets (included in plugin configuration).
- The plugin uses `--net=host` to access the host's physical network interface.
- Only IPv4 DHCP is supported (no IPv6/DHCPv6 yet).

## License

MIT
