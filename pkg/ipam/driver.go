// Package ipam implements the Docker IPAM driver interface for DHCP-based
// IP address allocation. Each container gets a unique IP from the LAN's
// DHCP server via a full DORA cycle.
package ipam

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"sync"

	"github.com/docker/go-plugins-helpers/ipam"
	"github.com/google/uuid"

	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/config"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/dhcp"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/iface"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/store"
)

// Driver implements ipam.Ipam providing DHCP-based IP address management.
type Driver struct {
	config      *config.Config
	ifaceInfo   *iface.Info
	dhcpClient  *dhcp.Client
	poolStore   *store.PoolStore
	leaseStore  *store.LeaseStore
	cancelFuncs map[string]context.CancelFunc // key: poolID+"|"+addr
	mu          sync.Mutex
}

// New creates a new Driver with the given dependencies.
func New(cfg *config.Config, info *iface.Info, client *dhcp.Client, ps *store.PoolStore, ls *store.LeaseStore) *Driver {
	return &Driver{
		config:      cfg,
		ifaceInfo:   info,
		dhcpClient:  client,
		poolStore:   ps,
		leaseStore:  ls,
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

// GetCapabilities returns the IPAM driver capabilities.
func (d *Driver) GetCapabilities() (*ipam.CapabilitiesResponse, error) {
	return &ipam.CapabilitiesResponse{
		RequiresMACAddress: true,
	}, nil
}

// GetDefaultAddressSpaces returns the default local and global address space names.
func (d *Driver) GetDefaultAddressSpaces() (*ipam.AddressSpacesResponse, error) {
	return &ipam.AddressSpacesResponse{
		LocalDefaultAddressSpace:  "dhcp-local",
		GlobalDefaultAddressSpace: "dhcp-global",
	}, nil
}

// RequestPool is called when a Docker network is created with this IPAM driver.
// It returns the auto-detected LAN subnet and gateway.
func (d *Driver) RequestPool(req *ipam.RequestPoolRequest) (*ipam.RequestPoolResponse, error) {
	if req.Pool != "" {
		// Verify the requested pool matches our detected subnet
		_, requested, err := net.ParseCIDR(req.Pool)
		if err != nil {
			return nil, fmt.Errorf("invalid requested pool %q: %w", req.Pool, err)
		}
		_, detected, err := net.ParseCIDR(d.ifaceInfo.Subnet)
		if err != nil {
			return nil, fmt.Errorf("invalid detected subnet %q: %w", d.ifaceInfo.Subnet, err)
		}
		if requested.String() != detected.String() {
			return nil, fmt.Errorf("requested pool %q does not match detected subnet %q",
				requested.String(), detected.String())
		}
	}

	poolID := uuid.New().String()
	d.poolStore.Add(poolID, &store.PoolInfo{
		Subnet:  d.ifaceInfo.Subnet,
		Gateway: d.ifaceInfo.Gateway,
	})

	resp := &ipam.RequestPoolResponse{
		PoolID: poolID,
		Pool:   d.ifaceInfo.Subnet,
		Data: map[string]string{
			"gateway":                   d.ifaceInfo.Gateway,
			"com.docker.network.gateway": d.ifaceInfo.Gateway,
		},
	}

	log.Printf("RequestPool: poolID=%s, subnet=%s, gateway=%s",
		poolID, d.ifaceInfo.Subnet, d.ifaceInfo.Gateway)
	return resp, nil
}

// ReleasePool is called when a Docker network is removed.
// It releases all outstanding DHCP leases for this pool.
func (d *Driver) ReleasePool(req *ipam.ReleasePoolRequest) error {
	leases := d.leaseStore.ReleaseAll(req.PoolID)
	for _, lease := range leases {
		if err := d.dhcpClient.Release(d.ifaceInfo.InterfaceName, lease); err != nil {
			log.Printf("ReleasePool: failed to release lease for pool %s: %v", req.PoolID, err)
		}
	}

	// Cancel all renewal goroutines for this pool
	d.mu.Lock()
	for key, cancel := range d.cancelFuncs {
		poolID, _, ok := splitCancelKey(key)
		if ok && poolID == req.PoolID {
			cancel()
			delete(d.cancelFuncs, key)
		}
	}
	d.mu.Unlock()

	d.poolStore.Remove(req.PoolID)
	log.Printf("ReleasePool: poolID=%s, released %d leases", req.PoolID, len(leases))
	return nil
}

// RequestAddress is called for each container being created on the network.
// It performs a DHCP DORA cycle to obtain an IP address from the LAN.
func (d *Driver) RequestAddress(req *ipam.RequestAddressRequest) (*ipam.RequestAddressResponse, error) {
	if req.Address != "" {
		return nil, fmt.Errorf("static address assignment not supported by DHCP IPAM driver")
	}

	log.Printf("RequestAddress: poolID=%s, options=%v", req.PoolID, req.Options)

	// Resolve MAC address: prefer from options, fall back to generated.
	mac := resolveMAC(req.Options, req.PoolID)
	log.Printf("RequestAddress: poolID=%s, using MAC=%s", req.PoolID, mac.String())

	// Perform DHCP DORA
	ctx, cancel := context.WithTimeout(context.Background(), d.config.DHCPTimeout)
	defer cancel()

	lease, err := d.dhcpClient.Obtain(ctx, d.ifaceInfo.InterfaceName, mac)
	if err != nil {
		return nil, fmt.Errorf("DHCP request failed: %w", err)
	}

	addr := lease.ACK.YourIPAddr.String()
	_, ipNet, err := net.ParseCIDR(d.ifaceInfo.Subnet)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to parse subnet %q: %w", d.ifaceInfo.Subnet, err)
	}
	ones, _ := ipNet.Mask.Size()
	cidrAddr := fmt.Sprintf("%s/%d", addr, ones)

	// Store the lease
	d.leaseStore.Add(req.PoolID, addr, lease)

	// Start renewal loop
	renewCtx, renewCancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.cancelFuncs[cancelKey(req.PoolID, addr)] = renewCancel
	d.mu.Unlock()

	go d.dhcpClient.RenewLoop(renewCtx, d.ifaceInfo.InterfaceName, lease, d.leaseStore, req.PoolID, addr)

	log.Printf("RequestAddress: poolID=%s, addr=%s, cidr=%s, MAC=%s",
		req.PoolID, addr, cidrAddr, mac.String())

	return &ipam.RequestAddressResponse{
		Address: cidrAddr,
		Data: map[string]string{
			"mac_address": mac.String(),
		},
	}, nil
}

// ReleaseAddress is called when a container is removed from the network.
// It sends a DHCPRELEASE for the lease and cleans up the renewal goroutine.
func (d *Driver) ReleaseAddress(req *ipam.ReleaseAddressRequest) error {
	addr := req.Address

	// Parse CIDR address to get just the IP
	ip, _, err := net.ParseCIDR(addr)
	if err == nil {
		addr = ip.String()
	}

	// Cancel the renewal goroutine
	d.mu.Lock()
	if cancel, ok := d.cancelFuncs[cancelKey(req.PoolID, addr)]; ok {
		cancel()
		delete(d.cancelFuncs, cancelKey(req.PoolID, addr))
	}
	d.mu.Unlock()

	// Release the DHCP lease
	lease := d.leaseStore.Get(req.PoolID, addr)
	if lease != nil {
		if err := d.dhcpClient.Release(d.ifaceInfo.InterfaceName, lease); err != nil {
			log.Printf("ReleaseAddress: failed to release DHCP lease for %s/%s: %v",
				req.PoolID, addr, err)
		}
	}

	d.leaseStore.Remove(req.PoolID, addr)
	log.Printf("ReleaseAddress: poolID=%s, addr=%s", req.PoolID, addr)
	return nil
}

// resolveMAC extracts a MAC address from Docker options or generates one.
// Docker may pass "mac" or "macaddress" in options when RequiresMACAddress is true.
func resolveMAC(opts map[string]string, poolID string) net.HardwareAddr {
	for _, key := range []string{"mac", "macaddress"} {
		if v, ok := opts[key]; ok && v != "" {
			if hw, err := net.ParseMAC(v); err == nil {
				return hw
			}
			log.Printf("resolveMAC: failed to parse %q from options[%q], falling back to generated", v, key)
		}
	}
	return generateMAC(poolID)
}

// generateMAC creates a deterministic, locally administered unicast MAC address
// for a container based on the pool ID. The prefix 02:1a:2b is a locally
// administered unicast OUI (bit 1 of first byte = 1 = local, bit 0 = 0 = unicast).
func generateMAC(poolID string) net.HardwareAddr {
	h := fnv.New32a()
	h.Write([]byte(poolID))
	sum := h.Sum32()
	return net.HardwareAddr{0x02, 0x1a, 0x2b,
		byte(sum >> 16), byte(sum >> 8), byte(sum)}
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

// Ensure Driver implements ipam.Ipam at compile time.
var _ ipam.Ipam = (*Driver)(nil)
