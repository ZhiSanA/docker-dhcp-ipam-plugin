package internal

import (
	"sync"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

// ── Pool Info & Store ─────────────────────────────────────────────────

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

// ── Lease Store ───────────────────────────────────────────────────────

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
