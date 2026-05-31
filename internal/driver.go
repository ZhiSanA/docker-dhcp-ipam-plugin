package internal

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/docker/go-plugins-helpers/ipam"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

// ── IPAM Driver ───────────────────────────────────────────────────────

type Driver struct {
	cfg         *Config
	iface       *InterfaceInfo
	dhcp        *DHCPClient
	dhcpv6      *DHCPv6Client
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
		dhcpv6:      newDHCPv6Client(cfg),
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

// matchPoolCIDR checks whether reqPool matches the host subnet for the given IP family.
// It returns the matching subnet string and gateway string, or an error.
func (d *Driver) matchPoolCIDR(reqPool string) (subnet, gateway string, err error) {
	_, requested, err := net.ParseCIDR(reqPool)
	if err != nil {
		return "", "", fmt.Errorf("invalid pool %q: %w", reqPool, err)
	}

	candidates := []struct {
		subnet  string
		gateway string
	}{
		{d.iface.SubnetV4, d.iface.GatewayV4},
	}
	if d.iface.SubnetV6 != "" {
		candidates = append(candidates, struct {
			subnet  string
			gateway string
		}{d.iface.SubnetV6, d.iface.GatewayV6})
	}

	for _, c := range candidates {
		if c.subnet == "" {
			continue
		}
		_, detected, err := net.ParseCIDR(c.subnet)
		if err != nil {
			continue
		}
		if requested.String() == detected.String() {
			return c.subnet, c.gateway, nil
		}
	}

	return "", "", fmt.Errorf("requested pool %q does not match detected subnet (v4=%q, v6=%q)", reqPool, d.iface.SubnetV4, d.iface.SubnetV6)
}

func (d *Driver) RequestPool(req *ipam.RequestPoolRequest) (*ipam.RequestPoolResponse, error) {
	if req.Pool == "" {
		return nil, fmt.Errorf("pool must be specified")
	}

	subnet, gateway, err := d.matchPoolCIDR(req.Pool)
	if err != nil {
		return nil, err
	}

	log.Printf("RequestPool: poolID=%s req.Pool=%q gateway=%s", subnet, req.Pool, gateway)
	return &ipam.RequestPoolResponse{
		PoolID: subnet,
		Pool:   subnet,
		Data:   map[string]string{"gateway": gateway},
	}, nil
}

func (d *Driver) ReleasePool(req *ipam.ReleasePoolRequest) error {
	log.Printf("ReleasePool: poolID=%s", req.PoolID)
	return nil
}

func (d *Driver) RequestAddress(req *ipam.RequestAddressRequest) (*ipam.RequestAddressResponse, error) {
	log.Printf("RequestAddress: poolID=%s address=%q options=%v", req.PoolID, req.Address, req.Options)

	// Determine the IP family from the poolID to pick the right gateway & subnet
	isV6 := false
	if poolIP, _, err := net.ParseCIDR(req.PoolID); err == nil && poolIP.To4() == nil {
		isV6 = true
	}

	var subnetCIDR string
	if isV6 {
		subnetCIDR = d.iface.SubnetV6
	} else {
		subnetCIDR = d.iface.SubnetV4
	}

	// Gateway — accept any gateway address with subnet prefix.
	if req.Address != "" {
		if req.Options["RequestAddressType"] == "com.docker.network.gateway" {
			_, ipNet, _ := net.ParseCIDR(subnetCIDR)
			ones, _ := ipNet.Mask.Size()
			cidrAddr := fmt.Sprintf("%s/%d", net.ParseIP(req.Address).String(), ones)
			log.Printf("RequestAddress: returning gateway addr=%s", cidrAddr)
			return &ipam.RequestAddressResponse{
				Address: cidrAddr,
			}, nil
		}
		log.Printf("RequestAddress: refusing static address %q", req.Address)
		return nil, fmt.Errorf("static address %q not supported by DHCP IPAM driver", req.Address)
	}

		// IPv6: try DHCPv6 first, fall back to EUI-64 from MAC.
	if isV6 {
			mac := resolveMAC(req.Options, req.PoolID, d.cfg)
			endpointName := req.Options["com.docker.network.endpoint.name"]
			ctx, cancel := context.WithTimeout(context.Background(), d.cfg.DHCPTimeout)
			defer cancel()

			reply, err := d.dhcpv6.Obtain6(ctx, d.iface.Name, mac, endpointName)
			if err == nil && reply != nil {
				// DHCPv6 succeeded â use assigned address.
				iaAddr := extractIPv6Addr(reply, subnetCIDR)
				log.Printf("RequestAddress: IPv6 pool (DHCPv6), MAC=%s addr=%s", mac, iaAddr)
				return &ipam.RequestAddressResponse{
					Address: iaAddr,
				}, nil
			}

			// No DHCPv6 server â fall back to EUI-64.
			ipv6Addr := eui64Address(subnetCIDR, mac)
			log.Printf("RequestAddress: IPv6 pool (EUI-64), MAC=%s addr=%s", mac, ipv6Addr)
			return &ipam.RequestAddressResponse{
				Address: ipv6Addr,
			}, nil
		}

	// No address specified — obtain via DHCP.
	mac := resolveMAC(req.Options, req.PoolID, d.cfg)
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.DHCPTimeout)
	defer cancel()

	lease, err := d.dhcp.Obtain(ctx, d.iface.Name, mac, "")
	if err != nil {
		return nil, fmt.Errorf("DHCP request failed: %w", err)
	}

	addr := lease.ACK.YourIPAddr.String()
	_, ipNet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse subnet %q: %w", subnetCIDR, err)
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

// extractIPv6Addr extracts the first assigned IPv6 address from a DHCPv6
// reply, and formats it as a CIDR using the subnet prefix length.
func extractIPv6Addr(reply *dhcpv6.Message, subnetCIDR string) string {
	iana := reply.Options.OneIANA()
	if iana != nil {
		iaAddr := iana.Options.OneAddress()
		if iaAddr != nil {
			_, ipNet, _ := net.ParseCIDR(subnetCIDR)
			ones, _ := ipNet.Mask.Size()
			return fmt.Sprintf("%s/%d", iaAddr.IPv6Addr.String(), ones)
		}
	}
	// Fallback: return the subnet itself if no address was assigned
	return subnetCIDR
}

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
