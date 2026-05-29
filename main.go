package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/docker/go-plugins-helpers/ipam"
)

func loadConfig() *Config {
	cfg := &Config{
		SocketPath:         defaultSocketPath,
		LogLevel:           defaultLogLevel,
		LeaseRenewInterval: 30 * time.Second,
		DHCPTimeout:        10 * time.Second,
		DHCPRetries:        3,
	}
	if v := os.Getenv(envInterface); v != "" {
		cfg.HostInterface = v
	}
	if v := os.Getenv(envSocketPath); v != "" {
		cfg.SocketPath = v
	}
	if v := os.Getenv(envRenewInterval); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.LeaseRenewInterval = d
		}
	}
	if v := os.Getenv(envDHCPTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DHCPTimeout = d
		}
	}
	if v := os.Getenv(envDHCPRetries); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DHCPRetries = n
		}
	}
	return cfg
}

func main() {
	cfg := loadConfig()
	log.Printf("starting DHCP IPAM plugin, interface=%s, socket=%s", cfg.HostInterface, cfg.SocketPath)

	ifaceInfo, err := detectInterface(cfg.HostInterface)
	if err != nil {
		log.Fatalf("failed to detect host interface: %v", err)
	}
	log.Printf("detected interface: %s (MAC=%s, subnet=%s)", ifaceInfo.Name, ifaceInfo.MAC, ifaceInfo.Subnet)

	if cfg.HostInterface == "" {
		cfg.HostInterface = ifaceInfo.Name
	}

	driver := &Driver{
		cfg:         cfg,
		iface:       ifaceInfo,
		dhcp:        newDHCPClient(cfg),
		pools:       NewPoolStore(),
		leases:      NewLeaseStore(),
		cancelFuncs: make(map[string]context.CancelFunc),
	}
	handler := ipam.NewHandler(driver)

	os.Remove(cfg.SocketPath)
	log.Printf("listening on %s", cfg.SocketPath)
	if err := handler.ServeUnix(cfg.SocketPath, 0); err != nil {
		log.Fatalf("failed to serve on %s: %v", cfg.SocketPath, err)
	}
}