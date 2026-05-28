# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

- `make build` — Build the plugin binary to `build/docker-dhcp-ipam-plugin`
- `make test` — Run all tests with race detection: `go test -v -count=1 -race ./pkg/...`
- `make fmt && make vet` — Format and vet all Go code
- `make plugin` — Build binary → create Docker rootfs → create & enable Docker managed plugin
- `make all` — fmt + vet + build
- `make clean` — Remove build artifacts
- Single test: `go test -v -run TestName ./pkg/package/`

## Architecture Overview

Docker IPAM plugin that allocates container IPs via DHCP from the host's LAN.

**Request flow:** Docker daemon → Unix socket → custom handler → IPAM driver → DHCP client → LAN

### Package layout

- `cmd/docker-dhcp-ipam-plugin/main.go` — Entry point. Creates a custom HTTP handler with lowercase `{"Implements":["ipamdriver"]}` manifest (Docker 29.x compatibility workaround). Registers all 6 IPAM API routes manually instead of using `go-plugins-helpers/ipam.NewHandler`.
- `pkg/ipam/driver.go` — Core `Driver` struct implementing `GetCapabilities`, `GetDefaultAddressSpaces`, `RequestPool`, `ReleasePool`, `RequestAddress`, `ReleaseAddress`. Uses `go-plugins-helpers/ipam` request/response types. Manages lease renewal goroutines via `cancelFuncs` map.
- `pkg/dhcp/client.go` — Wraps `github.com/insomniacslk/dhcp/dhcpv4/nclient4`. Provides `Obtain` (full DORA), `Renew`, `Release`, and background `RenewLoop` goroutine that renews at 80% of T1. Each call creates/frees a raw socket. Requires `CAP_NET_RAW`.
- `pkg/iface/detector.go` — Reads `/proc/net/route` for default route, resolves interface, extracts subnet CIDR, gateway IP, and MAC. Has a `detectWithRouteFile` helper for testing.
- `pkg/store/memory.go` — Thread-safe in-memory `PoolStore` (PoolID → Subnet/Gateway) and `LeaseStore` (PoolID+Addr → `*nclient4.Lease`). Both use `sync.RWMutex`.
- `pkg/config/config.go` — Reads env vars with defaults: `DHCP_IPAM_INTERFACE`, `DHCP_IPAM_SOCKET_PATH` (default `/run/docker/plugins/dhcp_ipam.sock`), log level, timeout, retries.

### Plugin packaging

- `config.json` — Docker plugin manifest, uses host networking, `ipamdriver` interface type, `CAP_NET_RAW` + `CAP_NET_ADMIN`
- `Dockerfile` — Multi-stage: golang:1.23-alpine builder → alpine:3.19 runtime (static binary)
- `Makefile` — `make plugin` builds rootfs and creates/enables the Docker managed plugin

### Known issues

- **Docker 29.x managed plugin capability bug:** `config.json` sets `"types": ["ipamdriver"]` but Docker internally stores it as `[".ipamdriver/"]` and then fails capability matching. Workaround pending — the plugin works correctly via direct API testing but cannot be used with `docker network create --ipam-driver dhcp-ipam` on Docker 29.1.3.
- **Go 1.23+ required** due to `insomniacslk/dhcp` dependency.