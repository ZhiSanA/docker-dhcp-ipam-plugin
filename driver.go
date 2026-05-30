package main

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/go-plugins-helpers/ipam"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

// ── Config ────────────────────────────────────────────────────────────

const (
	envInterface     = "DHCP_IPAM_INTERFACE"
	envSocketPath    = "DHCP_IPAM_SOCKET_PATH"
	envLogLevel      = "DHCP_IPAM_LOG_LEVEL"
	envRenewInterval = "DHCP_IPAM_RENEW_INTERVAL"
	envDHCPTimeout   = "DHCP_IPAM_TIMEOUT"
	envDHCPRetries   = "DHCP_IPAM_RETRIES"

	defaultSocketPath = "/run/docker/plugins/dhcp-ipam.sock"
	defaultLogLevel   = "info"
)

type Config struct {
	HostInterface      string
	SocketPath         string
	LogLevel           string
	LeaseRenewInterval time.Duration
	DHCPTimeout        time.Duration
	DHCPRetries        int
}

// ── Interface Detection ───────────────────────────────────────────────

type InterfaceInfo struct {
	Name    string
	MAC     net.HardwareAddr
	Subnet  string // CIDR, e.g. "192.168.1.0/24"
	Gateway string
	IP      net.IP
}

func detectInterface(ifaceName string) (*InterfaceInfo, error) {
	if ifaceName != "" {
		return detectNamedInterface(ifaceName)
	}
	// Auto-detect from default route
	return detectFromDefaultRoute()
}

func detectFromDefaultRoute() (*InterfaceInfo, error) {
	name, gatewayHex, err := parseProcNetRoute()
	if err != nil {
		return nil, err
	}
	info, err := resolveInterface(name)
	if err != nil {
		return nil, err
	}
	info.Gateway = parseGatewayHex(gatewayHex)
	return info, nil
}

func detectNamedInterface(name string) (*InterfaceInfo, error) {
	info, err := resolveInterface(name)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func resolveInterface(name string) (*InterfaceInfo, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("interface %q not found: %w", name, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("addresses for %q: %w", name, err)
	}
	var ipNet *net.IPNet
	for _, a := range addrs {
		ipNet, err = toIPv4Net(a)
		if err != nil {
			continue
		}
		break
	}
	if ipNet == nil {
		return nil, fmt.Errorf("interface %q has no IPv4 address", name)
	}
	ones, _ := ipNet.Mask.Size()
	network := ipNet.IP.Mask(ipNet.Mask)

	return &InterfaceInfo{
		Name:   iface.Name,
		MAC:    iface.HardwareAddr,
		Subnet: fmt.Sprintf("%s/%d", network.String(), ones),
		IP:     ipNet.IP,
	}, nil
}

// ── /proc/net/route parsing ───────────────────────────────────────────

func parseProcNetRoute() (ifaceName, gatewayHex string, err error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", "", fmt.Errorf("reading /proc/net/route: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 || strings.HasPrefix(line, "Iface") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[1] == "00000000" {
			return fields[0], fields[2], nil
		}
	}
	return "", "", fmt.Errorf("no default route found")
}

// ── DHCP Client ───────────────────────────────────────────────────────

type DHCPClient struct {
	timeout time.Duration
	retries int
}

func newDHCPClient(cfg *Config) *DHCPClient {
	return &DHCPClient{timeout: cfg.DHCPTimeout, retries: cfg.DHCPRetries}
}

func (c *DHCPClient) Obtain(ctx context.Context, ifaceName string, hwAddr net.HardwareAddr) (*nclient4.Lease, error) {
	cli, err := nclient4.New(ifaceName,
		nclient4.WithHWAddr(hwAddr),
		nclient4.WithTimeout(c.timeout),
		nclient4.WithRetry(c.retries),
	)
	if err != nil {
		return nil, fmt.Errorf("dhcp client: %w", err)
	}
	defer cli.Close()
	return cli.Request(ctx)
}

func (c *DHCPClient) Renew(ctx context.Context, ifaceName string, lease *nclient4.Lease) (*nclient4.Lease, error) {
	cli, err := nclient4.New(ifaceName,
		nclient4.WithHWAddr(lease.ACK.ClientHWAddr),
		nclient4.WithTimeout(c.timeout),
		nclient4.WithRetry(c.retries),
	)
	if err != nil {
		return nil, fmt.Errorf("dhcp client: %w", err)
	}
	defer cli.Close()
	return cli.Renew(ctx, lease)
}

func (c *DHCPClient) Release(ifaceName string, lease *nclient4.Lease) error {
	cli, err := nclient4.New(ifaceName,
		nclient4.WithHWAddr(lease.ACK.ClientHWAddr),
		nclient4.WithTimeout(c.timeout),
	)
	if err != nil {
		return fmt.Errorf("dhcp client: %w", err)
	}
	defer cli.Close()
	return cli.Release(lease)
}

func (c *DHCPClient) RenewLoop(ctx context.Context, ifaceName string, lease *nclient4.Lease, store *LeaseStore, poolID, addr string) {
	currentLease := lease
	for {
		sleepDur := nextRenewalDelay(currentLease, c.timeout)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleepDur):
		}
		newLease, err := c.Renew(ctx, ifaceName, currentLease)
		if err != nil {
			log.Printf("renew failed for %s/%s: %v (retry in 30s)", poolID, addr, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		store.Update(poolID, addr, newLease)
		currentLease = newLease
	}
}

func nextRenewalDelay(lease *nclient4.Lease, timeout time.Duration) time.Duration {
	t1 := lease.ACK.IPAddressRenewalTime(0)
	if t1 > 0 {
		d := time.Duration(float64(t1) * 0.8)
		if d < time.Second {
			d = time.Second
		}
		return d
	}
	lt := lease.ACK.IPAddressLeaseTime(0)
	if lt > 0 {
		return time.Duration(float64(lt) * 0.4)
	}
	return 5 * time.Minute
}

// ── In-Memory Store ───────────────────────────────────────────────────

type PoolInfo struct {
	Subnet  string
	Gateway string
}

type PoolStore struct {
	mu    sync.RWMutex
	pools map[string]*PoolInfo
}

func NewPoolStore() *PoolStore {
	return &PoolStore{pools: make(map[string]*PoolInfo)}
}

func (s *PoolStore) Add(id string, info *PoolInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pools[id] = info
}

func (s *PoolStore) Get(id string) (*PoolInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pools[id]
	return p, ok
}

func (s *PoolStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pools, id)
}

func (s *PoolStore) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.pools[id]
	return ok
}

type LeaseStore struct {
	mu     sync.RWMutex
	leases map[string]map[string]*nclient4.Lease
}

func NewLeaseStore() *LeaseStore {
	return &LeaseStore{leases: make(map[string]map[string]*nclient4.Lease)}
}

func (s *LeaseStore) Add(poolID, addr string, lease *nclient4.Lease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] == nil {
		s.leases[poolID] = make(map[string]*nclient4.Lease)
	}
	s.leases[poolID][addr] = lease
}

func (s *LeaseStore) Get(poolID, addr string) *nclient4.Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leases[poolID] == nil {
		return nil
	}
	return s.leases[poolID][addr]
}

func (s *LeaseStore) Remove(poolID, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] != nil {
		delete(s.leases[poolID], addr)
		if len(s.leases[poolID]) == 0 {
			delete(s.leases, poolID)
		}
	}
}

func (s *LeaseStore) ReleaseAll(poolID string) []*nclient4.Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] == nil {
		return nil
	}
	released := make([]*nclient4.Lease, 0, len(s.leases[poolID]))
	for _, l := range s.leases[poolID] {
		released = append(released, l)
	}
	delete(s.leases, poolID)
	return released
}

func (s *LeaseStore) Update(poolID, addr string, lease *nclient4.Lease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] == nil {
		s.leases[poolID] = make(map[string]*nclient4.Lease)
	}
	s.leases[poolID][addr] = lease
}

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

	// Gateway — return as-is with subnet prefix.
	if req.Address != "" {
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
	mac := resolveMAC(req.Options, req.PoolID)
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

func resolveMAC(opts map[string]string, poolID string) net.HardwareAddr {
	for _, key := range []string{"mac", "macaddress"} {
		if v, ok := opts[key]; ok && v != "" {
			if hw, err := net.ParseMAC(v); err == nil {
				return hw
			}
		}
	}
	return generateMAC(poolID)
}

func generateMAC(poolID string) net.HardwareAddr {
	h := fnv.New32a()
	h.Write([]byte(poolID))
	sum := h.Sum32()
	return net.HardwareAddr{0x02, 0x1a, 0x2b, byte(sum >> 16), byte(sum >> 8), byte(sum)}
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

func toIPv4Net(addr net.Addr) (*net.IPNet, error) {
	ipNet, ok := addr.(*net.IPNet)
	if !ok {
		return nil, fmt.Errorf("not an IPNet")
	}
	if ipNet.IP.To4() == nil || ipNet.IP.IsLoopback() {
		return nil, fmt.Errorf("not IPv4 or loopback")
	}
	return ipNet, nil
}

func parseGatewayHex(h string) string {
	if len(h) < 8 {
		return ""
	}
	b := make([]byte, 4)
	for i := 0; i < 4; i++ {
		v := byte(0)
		for j := 0; j < 2; j++ {
			c := h[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				v = v*16 + (c - '0')
			case c >= 'a' && c <= 'f':
				v = v*16 + (c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				v = v*16 + (c - 'A' + 10)
			}
		}
		b[3-i] = v
	}
	return net.IPv4(b[0], b[1], b[2], b[3]).String()
}
