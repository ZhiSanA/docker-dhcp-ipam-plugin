package internal

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/dhcpv6/nclient6"
)

// ── DHCP Client (IPv4) ───────────────────────────────────────────────

type DHCPClient struct {
	timeout time.Duration
	retries int
}

func newDHCPClient(cfg *Config) *DHCPClient {
	return &DHCPClient{timeout: cfg.DHCPTimeout, retries: cfg.DHCPRetries}
}

func (c *DHCPClient) Obtain(ctx context.Context, ifaceName string, hwAddr net.HardwareAddr, hostname string) (*nclient4.Lease, error) {
	cli, err := nclient4.New(ifaceName,
		nclient4.WithHWAddr(hwAddr),
		nclient4.WithTimeout(c.timeout),
		nclient4.WithRetry(c.retries),
	)
	if err != nil {
		return nil, fmt.Errorf("dhcp client: %w", err)
	}
	defer cli.Close()

	mods := []dhcpv4.Modifier{}
	if hostname != "" {
		mods = append(mods, dhcpv4.WithGeneric(dhcpv4.OptionHostName, []byte(hostname)))
	}
	return cli.Request(ctx, mods...)
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

// ── Renewal Loop (IPv4) ──────────────────────────────────────────────

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

// ── DHCP Client (IPv6) ───────────────────────────────────────────────

type DHCPv6Client struct {
	timeout time.Duration
	retries int
}

func newDHCPv6Client(cfg *Config) *DHCPv6Client {
	return &DHCPv6Client{timeout: cfg.DHCPTimeout, retries: cfg.DHCPRetries}
}

// Obtain6 performs DHCPv6 Solicit → Request to get an IPv6 address.
// Falls back to EUI-64 if no DHCPv6 server responds (returns nil, nil).
func (c *DHCPv6Client) Obtain6(ctx context.Context, ifaceName string, hwAddr net.HardwareAddr, hostname string) (*dhcpv6.Message, error) {
	cli, err := nclient6.New(ifaceName,
		nclient6.WithTimeout(c.timeout),
		nclient6.WithRetry(c.retries),
	)
	if err != nil {
		return nil, fmt.Errorf("dhcpv6 client: %w", err)
	}
	defer cli.Close()

	// Build DUID from MAC (DUID-LL)
	duid, err := dhcpv6.GetDUIDLL()
	if err != nil {
		return nil, fmt.Errorf("dhcpv6 duid: %w", err)
	}
	mods := []dhcpv6.Modifier{
		dhcpv6.WithClientID(duid),
	}
	if hostname != "" {
		mods = append(mods, dhcpv6.WithFQDN(0, hostname))
	}

	advertise, err := cli.Solicit(ctx, mods...)
	if err != nil {
		// No DHCPv6 server — return nil so caller can fall back to EUI-64
		return nil, nil
	}

	reply, err := cli.Request(ctx, advertise, mods...)
	if err != nil {
		return nil, nil
	}

	return reply, nil
}