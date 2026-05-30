package internal

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

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

// ── Renewal Loop ──────────────────────────────────────────────────────

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