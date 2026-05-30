package main

import (
	"context"
	"log"
	"os"

	"github.com/docker/go-plugins-helpers/ipam"
)

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