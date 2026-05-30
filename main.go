package main

import (
	"log"
	"os"

	"github.com/docker/go-plugins-helpers/ipam"
	"github.com/tuzi/docker-dhcp-ipam-plugin/internal"
)

func main() {
	cfg := internal.LoadConfig()
	log.Printf("starting DHCP IPAM plugin, interface=%s, socket=%s", cfg.HostInterface, cfg.SocketPath)

	ifaceInfo, err := internal.DetectInterface(cfg.HostInterface)
	if err != nil {
		log.Fatalf("failed to detect host interface: %v", err)
	}
	log.Printf("detected interface: %s (MAC=%s, subnet=%s)", ifaceInfo.Name, ifaceInfo.MAC, ifaceInfo.Subnet)

	if cfg.HostInterface == "" {
		cfg.HostInterface = ifaceInfo.Name
	}

	driver := internal.NewDriver(cfg, ifaceInfo)
	handler := ipam.NewHandler(driver)

	os.Remove(cfg.SocketPath)
	log.Printf("listening on %s", cfg.SocketPath)
	if err := handler.ServeUnix(cfg.SocketPath, 0); err != nil {
		log.Fatalf("failed to serve on %s: %v", cfg.SocketPath, err)
	}
}