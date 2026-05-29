# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

- `make build` — Build the plugin binary to `build/docker-dhcp-ipam-plugin`
- `make plugin` — Build binary → create Docker rootfs → create & enable Docker managed plugin
- `make clean` — Remove build artifacts

## Architecture Overview

Docker IPAM plugin that allocates container IPs via DHCP from the host's LAN. Everything lives in a single `cmd/docker-dhcp-ipam-plugin/` package (two files).

**Request flow:** Docker daemon → Unix socket → `ipam.NewHandler` → `Driver` → DHCP client → LAN

### Files

- `cmd/docker-dhcp-ipam-plugin/main.go` — Entry point. Loads config, detects interface, creates driver, starts Unix socket server.
- `cmd/docker-dhcp-ipam-plugin/driver.go` — Everything else: config struct, interface detection (reads `/proc/net/route`), DHCP client wrapper (uses `nclient4`), in-memory `PoolStore`/`LeaseStore`, and the `Driver` struct implementing `ipam.Ipam`.

### Plugin packaging

- `config.json` — Docker plugin manifest, uses host networking, `docker.ipamdriver/1.0` type, `CAP_NET_RAW` + `CAP_NET_ADMIN`
- `Dockerfile` — Multi-stage: golang:1.23-alpine builder → alpine runtime
- `Makefile` — `make plugin` builds rootfs and creates/enables the Docker managed plugin

### Known issues

- **Docker 29.x managed plugin capability bug:** `config.json` sets `"types": ["docker.ipamdriver/1.0"]` but Docker may still fail capability matching. Workaround: run the plugin standalone (see test-plugin.sh flow).
- **Go 1.23+ required** due to `insomniacslk/dhcp` dependency.