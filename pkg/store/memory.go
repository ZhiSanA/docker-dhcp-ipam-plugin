// Package store provides thread-safe in-memory stores for managing DHCP
// address pools and their associated leases. It is a foundational package
// used by the IPAM driver to track pool metadata and active DHCP leases.
package store

import (
	"sync"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

// PoolInfo stores metadata about an address pool (subnet).
type PoolInfo struct {
	// Subnet is the CIDR notation of the pool, e.g. "192.168.1.0/24".
	Subnet string
	// Gateway is the default gateway IP for the pool, e.g. "192.168.1.1".
	Gateway string
}

// PoolStore tracks address pools by PoolID. It is safe for concurrent use.
type PoolStore struct {
	mu    sync.RWMutex
	pools map[string]*PoolInfo
}

// NewPoolStore creates and returns a new, empty PoolStore.
func NewPoolStore() *PoolStore {
	return &PoolStore{
		pools: make(map[string]*PoolInfo),
	}
}

// Add inserts or updates the pool identified by poolID.
func (s *PoolStore) Add(poolID string, info *PoolInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pools[poolID] = info
}

// Get retrieves the pool identified by poolID. The bool indicates whether
// the pool was found.
func (s *PoolStore) Get(poolID string) (*PoolInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.pools[poolID]
	if !ok {
		return nil, false
	}
	return info, true
}

// Remove deletes the pool identified by poolID. It is a no-op if the pool
// does not exist.
func (s *PoolStore) Remove(poolID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pools, poolID)
}

// List returns a shallow copy of the internal pools map. Callers may safely
// iterate or modify the returned map without affecting PoolStore state.
func (s *PoolStore) List() map[string]*PoolInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := make(map[string]*PoolInfo, len(s.pools))
	for k, v := range s.pools {
		copy[k] = v
	}
	return copy
}

// LeaseStore tracks active DHCP leases. It is safe for concurrent use.
// The internal key structure is poolID -> address -> *nclient4.Lease.
type LeaseStore struct {
	mu     sync.RWMutex
	leases map[string]map[string]*nclient4.Lease
}

// NewLeaseStore creates and returns a new, empty LeaseStore.
func NewLeaseStore() *LeaseStore {
	return &LeaseStore{
		leases: make(map[string]map[string]*nclient4.Lease),
	}
}

// Add stores a lease for the given pool and address.
func (s *LeaseStore) Add(poolID, addr string, lease *nclient4.Lease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] == nil {
		s.leases[poolID] = make(map[string]*nclient4.Lease)
	}
	s.leases[poolID][addr] = lease
}

// Get retrieves the lease for the given pool and address, or nil if not found.
func (s *LeaseStore) Get(poolID, addr string) *nclient4.Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leases[poolID] == nil {
		return nil
	}
	return s.leases[poolID][addr]
}

// Remove deletes the lease for the given pool and address. It is a no-op if
// the lease does not exist.
func (s *LeaseStore) Remove(poolID, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] != nil {
		delete(s.leases[poolID], addr)
		// Clean up empty inner map.
		if len(s.leases[poolID]) == 0 {
			delete(s.leases, poolID)
		}
	}
}

// ReleaseAll removes and returns all leases for the specified pool.
// Returns nil if the pool has no leases.
func (s *LeaseStore) ReleaseAll(poolID string) []*nclient4.Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] == nil {
		return nil
	}
	released := make([]*nclient4.Lease, 0, len(s.leases[poolID]))
	for _, lease := range s.leases[poolID] {
		released = append(released, lease)
	}
	delete(s.leases, poolID)
	return released
}

// Update replaces the lease for the given pool and address.
func (s *LeaseStore) Update(poolID, addr string, lease *nclient4.Lease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[poolID] == nil {
		s.leases[poolID] = make(map[string]*nclient4.Lease)
	}
	s.leases[poolID][addr] = lease
}