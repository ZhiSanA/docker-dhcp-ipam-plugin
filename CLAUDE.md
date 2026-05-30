# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

- `go build -o docker-dhcp-ipam-plugin .` — Build standalone binary
- `CGO_ENABLED=0 GOARCH=amd64 go build -ldflags="-s -w" -o build/docker-dhcp-ipam-plugin .` — Production-style binary build
- `./build.sh` — Build binary → create Docker rootfs → create & enable Docker managed plugin
- `./build.sh clean` — Remove the managed plugin
- `docker plugin install <name>` — Install pre-built plugin (see README)
- No tests exist yet; `go test ./...` runs nothing

## Architecture

Docker IPAM plugin that allocates container IPs via DHCP from the host's LAN. Single Go module (`github.com/tuzi/docker-dhcp-ipam-plugin`), no `cmd/` subdirectory — `main.go` at root, all logic in `internal/` package.

**Request flow:** Docker daemon → Unix socket → `ipam.NewHandler(driver)` → `Driver` methods → `DHCPClient` → LAN DHCP server

### Key files

- `main.go` — Entry point. Loads config, detects host interface (reads `/proc/net/route` for default route), creates `Driver`, starts Unix socket server via `go-plugins-helpers/ipam`.
- `internal/config.go` — `Config` struct and `LoadConfig()` reading env vars (`DHCP_IPAM_INTERFACE`, `DHCP_IPAM_TIMEOUT`, `DHCP_IPAM_MAC_FROM_NAME`, etc.).
- `internal/interface.go` — `DetectInterface()` reads `/proc/net/route` to find the default route interface, then resolves its MAC, subnet CIDR, and gateway IP via `net.Interface`.
- `internal/driver.go` — `Driver` struct implementing `ipam.Ipam`. Key methods: `RequestPool` (validates requested subnet matches detected subnet), `RequestAddress` (obtains DHCP lease, stores it, starts background renew goroutine), `ReleasePool`/`ReleaseAddress` (no-ops — DHCP lease lifecycle is managed by the renew loop).
- `internal/dhcp.go` — `DHCPClient` wrapping `nclient4`. Methods: `Obtain`, `Renew`, `Release`, `RenewLoop` (background goroutine that re-renters at ~80% of renewal time / 40% of lease time).
- `internal/store.go` — Thread-safe in-memory `PoolStore` and `LeaseStore` (sync.RWMutex + maps).
- `internal/mac.go` — MAC address resolution: (1) FNV-32a hash of container endpoint name for stable MAC, (2) Docker-provided MAC from options, (3) fallback hash of poolID.

### Plugin packaging

- `config.json` — Docker managed plugin manifest (host networking, `docker.ipamdriver/1.0`, `CAP_NET_RAW` + `CAP_NET_ADMIN`)
- `build.sh` — Builds binary in temp dir, creates `docker plugin create` / `docker plugin enable`

### Known issues

- **No Docker managed plugin capability workaround in `config.json`:** Docker 29.x may fail capability matching for `docker.ipamdriver/1.0`. Run the plugin as a standalone binary as a fallback.
- **Go 1.23+ required** due to `insomniacslk/dhcp` dependency.
- **No tests exist** — any new code should add coverage.
- **`ReleaseAddress` is a no-op** — leases are never explicitly released to the DHCP server when a container stops; the goroutine is just left to expire.
