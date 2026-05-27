// Package dhcp provides a DHCP client wrapper that performs DORA, renew,
// release, and automatic lease renewal for the Docker DHCP IPAM plugin.
//
// It wraps github.com/insomniacslk/dhcp/dhcpv4/nclient4 and creates a fresh
// nclient4.Client per operation for isolation. Each call opens a raw socket
// and therefore requires CAP_NET_RAW.
package dhcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"

	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/config"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/store"
)

// defaultRenewCheckInterval is used as the sleep cycle when the DHCP server
// does not supply a T1 (Renewal Time) option or the lease duration is zero.
const defaultRenewCheckInterval = 5 * time.Minute

// retryInterval is the shortened sleep between renewal attempts after a
// transient failure.
const retryInterval = 30 * time.Second

// t1Fraction is the fraction of T1 to wait before attempting a renewal.
// Renewing at 80% of T1 provides a safety margin.
const t1Fraction = 0.8

// Client wraps nclient4 functionality and configuration for DHCP operations.
type Client struct {
	cfg *config.Config
}

// NewClient creates a new DHCP Client with the given configuration.
func NewClient(cfg *config.Config) *Client {
	return &Client{cfg: cfg}
}

// newNClient creates a fresh nclient4.Client bound to the given network
// interface and hardware address. The caller must close the client when
// finished.
func (c *Client) newNClient(ifaceName string, hwAddr net.HardwareAddr) (*nclient4.Client, error) {
	opts := []nclient4.ClientOpt{
		nclient4.WithHWAddr(hwAddr),
		nclient4.WithTimeout(c.cfg.DHCPTimeout),
		nclient4.WithRetry(c.cfg.DHCPRetries),
	}
	return nclient4.New(ifaceName, opts...)
}

// Obtain performs a full DHCP DORA (Discover-Offer-Request-Ack) cycle on the
// specified network interface using the given hardware address.
func (c *Client) Obtain(ctx context.Context, ifaceName string, hwAddr net.HardwareAddr) (*nclient4.Lease, error) {
	client, err := c.newNClient(ifaceName, hwAddr)
	if err != nil {
		return nil, fmt.Errorf("dhcp: create client for obtain: %w", err)
	}
	defer client.Close()

	lease, err := client.Request(ctx)
	if err != nil {
		return nil, fmt.Errorf("dhcp: obtain (DORA) failed on %s: %w", ifaceName, err)
	}
	return lease, nil
}

// Renew renews an existing lease by sending a DHCPREQUEST to the server that
// originally granted the lease.
func (c *Client) Renew(ctx context.Context, ifaceName string, lease *nclient4.Lease) (*nclient4.Lease, error) {
	// Use the hardware address from the existing lease.
	hwAddr := lease.ACK.ClientHWAddr

	client, err := c.newNClient(ifaceName, hwAddr)
	if err != nil {
		return nil, fmt.Errorf("dhcp: create client for renew: %w", err)
	}
	defer client.Close()

	newLease, err := client.Renew(ctx, lease)
	if err != nil {
		return nil, fmt.Errorf("dhcp: renew failed on %s: %w", ifaceName, err)
	}
	return newLease, nil
}

// Release sends a DHCPRELEASE for the given lease. It does not take a context
// because a release is a fire-and-forget notification.
func (c *Client) Release(ifaceName string, lease *nclient4.Lease) error {
	hwAddr := lease.ACK.ClientHWAddr

	client, err := c.newNClient(ifaceName, hwAddr)
	if err != nil {
		return fmt.Errorf("dhcp: create client for release: %w", err)
	}
	defer client.Close()

	if err := client.Release(lease); err != nil {
		return fmt.Errorf("dhcp: release failed on %s: %w", ifaceName, err)
	}
	return nil
}

// RenewLoop runs in a background goroutine, periodically checking the lease
// T1 (Renewal Time) and renewing before expiry. It blocks until ctx is
// cancelled.
//
// On successful renewal the leaseStore is updated. On NAK or unrecoverable
// failure the lease is removed from the store.
func (c *Client) RenewLoop(ctx context.Context, ifaceName string, lease *nclient4.Lease, leaseStore *store.LeaseStore, poolID, addr string) {
	log.Printf("[dhcp] renew loop starting for %s on %s (pool=%s)", addr, ifaceName, poolID)

	// Use a copy of the lease pointer so we can update it on each renewal.
	currentLease := lease

	for {
		// Determine how long to sleep before the next renewal check.
		sleepDuration := c.nextRenewalDelay(currentLease)

		select {
		case <-ctx.Done():
			log.Printf("[dhcp] renew loop stopped for %s: %v", addr, ctx.Err())
			return
		case <-time.After(sleepDuration):
			// Proceed with renewal attempt.
		}

		newLease, err := c.Renew(ctx, ifaceName, currentLease)
		if err != nil {
			// Check if this is a NAK (server explicitly rejected the renew).
			var nakErr *nclient4.ErrNak
			if errors.As(err, &nakErr) {
				log.Printf("[dhcp] NAK received for %s on %s, removing lease: %v", addr, ifaceName, nakErr)
				leaseStore.Remove(poolID, addr)
				return
			}

			// Transient failure -- log and retry after a short interval.
			log.Printf("[dhcp] renew failed for %s on %s (will retry in %s): %v", addr, ifaceName, retryInterval, err)

			// Use a short retry interval instead of normal check cycle.
			select {
			case <-ctx.Done():
				log.Printf("[dhcp] renew loop stopped for %s: %v", addr, ctx.Err())
				return
			case <-time.After(retryInterval):
			}
			continue
		}

		// Success -- update the lease in the store and our local pointer.
		leaseStore.Update(poolID, addr, newLease)
		currentLease = newLease
		log.Printf("[dhcp] successfully renewed lease for %s on %s", addr, ifaceName)
	}
}

// nextRenewalDelay computes the duration to sleep before the next renewal
// attempt. It uses 80 % of the T1 (Renewal Time) value from the lease ACK.
// If T1 is not set or the lease duration is zero, it falls back to
// defaultRenewCheckInterval.
func (c *Client) nextRenewalDelay(lease *nclient4.Lease) time.Duration {
	t1 := lease.ACK.IPAddressRenewalTime(0)
	leaseTime := lease.ACK.IPAddressLeaseTime(0)

	if t1 > 0 && leaseTime > 0 {
		delay := time.Duration(float64(t1) * t1Fraction)
		// Clamp to a minimum of 1 second to avoid busy looping.
		if delay < time.Second {
			delay = time.Second
		}
		return delay
	}

	// Fallback: use the configured lease renew interval if available,
	// otherwise use the hard-coded default.
	interval := c.cfg.LeaseRenewInterval
	if interval <= 0 {
		interval = defaultRenewCheckInterval
	}

	// Also check whether the DHCP server provided a lease time but no T1.
	// In that case use 50 % of the lease time as a reasonable heuristic.
	if leaseTime > 0 {
		delay := time.Duration(float64(leaseTime) * 0.5)
		if delay < interval {
			return delay
		}
	}

	return interval
}

// IsNakError returns true if the given error is a DHCP NAK.
func IsNakError(err error) bool {
	var nakErr *nclient4.ErrNak
	return errors.As(err, &nakErr)
}
