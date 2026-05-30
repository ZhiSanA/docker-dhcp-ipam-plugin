package internal

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/docker/go-plugins-helpers/ipam"
)

// ── IPAM Driver ───────────────────────────────────────────────────────

type Driver struct {
	cfg         *Config
	iface       *InterfaceInfo
	dhcp        *DHCPClient
	pools       *PoolStore
	leases      *LeaseStore
	cancelFuncs map[string]context.CancelFunc
	mu          sync.Mutex
}

func NewDriver(cfg *Config, iface *InterfaceInfo) *Driver {
	return &Driver{
		cfg:         cfg,
		iface:       iface,
		dhcp:        newDHCPClient(cfg),
		pools:       NewPoolStore(),
		leases:      NewLeaseStore(),
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

func (d *Driver) GetCapabilities() (*ipam.CapabilitiesResponse, error) {
	log.Printf("GetCapabilities")
	resp := &ipam.CapabilitiesResponse{RequiresMACAddress: true}
	log.Printf("GetCapabilities: RequiresMACAddress=%v", resp.RequiresMACAddress)
	return resp, nil
}

func (d *Driver) GetDefaultAddressSpaces() (*ipam.AddressSpacesResponse, error) {
	log.Printf("GetDefaultAddressSpaces")
	resp := &ipam.AddressSpacesResponse{
		LocalDefaultAddressSpace:  "dhcp-local",
		GlobalDefaultAddressSpace: "dhcp-global",
	}
	log.Printf("GetDefaultAddressSpaces: local=%s global=%s", resp.LocalDefaultAddressSpace, resp.GlobalDefaultAddressSpace)
	return resp, nil
}

func (d *Driver) RequestPool(req *ipam.RequestPoolRequest) (*ipam.RequestPoolResponse, error) {
	if req.Pool == "" {
		return nil, fmt.Errorf("pool must be specified")
	}
	_, requested, err := net.ParseCIDR(req.Pool)
	if err != nil {
		return nil, fmt.Errorf("invalid pool %q: %w", req.Pool, err)
	}
	_, detected, err := net.ParseCIDR(d.iface.Subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid detected subnet %q: %w", d.iface.Subnet, err)
	}
	if requested.String() != detected.String() {
		return nil, fmt.Errorf("requested pool %q != detected subnet %q", requested.String(), detected.String())
	}

	log.Printf("RequestPool: poolID=%s req.Pool=%q gateway=%s", d.iface.Subnet, req.Pool, d.iface.Gateway)
	return &ipam.RequestPoolResponse{
		PoolID: d.iface.Subnet,
		Pool:   d.iface.Subnet,
		Data:   map[string]string{"gateway": d.iface.Gateway},
	}, nil
}

func (d *Driver) ReleasePool(req *ipam.ReleasePoolRequest) error {
	log.Printf("ReleasePool: poolID=%s", req.PoolID)
	return nil
}

func (d *Driver) RequestAddress(req *ipam.RequestAddressRequest) (*ipam.RequestAddressResponse, error) {
	log.Printf("RequestAddress: poolID=%s address=%q options=%v", req.PoolID, req.Address, req.Options)

	// Gateway — verify RequestAddressType and return as-is with subnet prefix.
	if req.Address != "" {
		if req.Options["RequestAddressType"] != "com.docker.network.gateway" {
			log.Printf("RequestAddress: refusing static address %q", req.Address)
			return nil, fmt.Errorf("static address %q not supported by DHCP IPAM driver", req.Address)
		}
		if ip := net.ParseIP(req.Address); ip != nil && ip.Equal(net.ParseIP(d.iface.Gateway)) {
			_, ipNet, _ := net.ParseCIDR(d.iface.Subnet)
			ones, _ := ipNet.Mask.Size()
			cidrAddr := fmt.Sprintf("%s/%d", ip.String(), ones)
			log.Printf("RequestAddress: returning gateway addr=%s", cidrAddr)
			return &ipam.RequestAddressResponse{
				Address: cidrAddr,
			}, nil
		}
		log.Printf("RequestAddress: refusing static address %q", req.Address)
		return nil, fmt.Errorf("static address %q not supported by DHCP IPAM driver", req.Address)
	}

	// No address specified — obtain via DHCP.
	mac := resolveMAC(req.Options, req.PoolID, d.cfg)
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.DHCPTimeout)
	defer cancel()

	lease, err := d.dhcp.Obtain(ctx, d.iface.Name, mac)
	if err != nil {
		return nil, fmt.Errorf("DHCP request failed: %w", err)
	}

	addr := lease.ACK.YourIPAddr.String()
	_, ipNet, err := net.ParseCIDR(d.iface.Subnet)
	if err != nil {
		return nil, fmt.Errorf("parse subnet %q: %w", d.iface.Subnet, err)
	}
	ones, _ := ipNet.Mask.Size()
	cidrAddr := fmt.Sprintf("%s/%d", addr, ones)

	d.leases.Add(req.PoolID, addr, lease)

	renewCtx, renewCancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.cancelFuncs[cancelKey(req.PoolID, addr)] = renewCancel
	d.mu.Unlock()
	go d.dhcp.RenewLoop(renewCtx, d.iface.Name, lease, d.leases, req.PoolID, addr)

	log.Printf("RequestAddress: poolID=%s addr=%s cidr=%s MAC=%s", req.PoolID, addr, cidrAddr, mac)
	return &ipam.RequestAddressResponse{
		Address: cidrAddr,
		Data:    map[string]string{"mac_address": mac.String()},
	}, nil
}

func (d *Driver) ReleaseAddress(req *ipam.ReleaseAddressRequest) error {
	log.Printf("ReleaseAddress: poolID=%s addr=%s", req.PoolID, req.Address)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────

func cancelKey(poolID, addr string) string {
	return poolID + "|" + addr
}

func splitCancelKey(key string) (poolID, addr string, ok bool) {
	for i, c := range key {
		if c == '|' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}
