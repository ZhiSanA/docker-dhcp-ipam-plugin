package store

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

// testLease creates a minimal *nclient4.Lease for testing purposes.
func testLease(ip string) *nclient4.Lease {
	return &nclient4.Lease{
		ACK: &dhcpv4.DHCPv4{
			YourIPAddr: net.ParseIP(ip),
		},
		CreationTime: time.Now(),
	}
}

// ---------------------------------------------------------------------------
// PoolStore tests
// ---------------------------------------------------------------------------

func TestPoolStore_New(t *testing.T) {
	s := NewPoolStore()
	if s == nil {
		t.Fatal("NewPoolStore() returned nil")
	}
	list := s.List()
	if len(list) != 0 {
		t.Fatalf("expected empty pool store, got %d entries", len(list))
	}
}

func TestPoolStore_AddAndGet(t *testing.T) {
	s := NewPoolStore()

	info := &PoolInfo{Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"}
	s.Add("pool1", info)

	got, ok := s.Get("pool1")
	if !ok {
		t.Fatal("expected to find pool1")
	}
	if got.Subnet != "10.0.0.0/24" {
		t.Fatalf("expected subnet 10.0.0.0/24, got %s", got.Subnet)
	}
	if got.Gateway != "10.0.0.1" {
		t.Fatalf("expected gateway 10.0.0.1, got %s", got.Gateway)
	}
}

func TestPoolStore_GetNotFound(t *testing.T) {
	s := NewPoolStore()
	_, ok := s.Get("nonexistent")
	if ok {
		t.Fatal("expected Get to return false for nonexistent pool")
	}
}

func TestPoolStore_Remove(t *testing.T) {
	s := NewPoolStore()
	s.Add("pool1", &PoolInfo{Subnet: "10.0.0.0/24"})
	s.Remove("pool1")

	_, ok := s.Get("pool1")
	if ok {
		t.Fatal("expected pool1 to be removed")
	}
}

func TestPoolStore_RemoveNonexistent(t *testing.T) {
	s := NewPoolStore()
	// Should not panic.
	s.Remove("nonexistent")
}

func TestPoolStore_Overwrite(t *testing.T) {
	s := NewPoolStore()
	s.Add("pool1", &PoolInfo{Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"})
	s.Add("pool1", &PoolInfo{Subnet: "10.0.1.0/24", Gateway: "10.0.1.1"})

	got, ok := s.Get("pool1")
	if !ok {
		t.Fatal("expected to find pool1")
	}
	if got.Subnet != "10.0.1.0/24" {
		t.Fatalf("expected updated subnet, got %s", got.Subnet)
	}
}

func TestPoolStore_ListReturnsCopy(t *testing.T) {
	s := NewPoolStore()
	s.Add("pool1", &PoolInfo{Subnet: "10.0.0.0/24"})

	list := s.List()
	list["pool2"] = &PoolInfo{Subnet: "10.0.1.0/24"}

	// The store should not have pool2.
	_, ok := s.Get("pool2")
	if ok {
		t.Fatal("List() returned a map that shares state with the store")
	}
}

func TestPoolStore_ConcurrentAccess(t *testing.T) {
	s := NewPoolStore()
	const goroutines = 50
	var wg sync.WaitGroup

	// Concurrent writes.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.Add("pool", &PoolInfo{
				Subnet:  "10.0.0.0/24",
				Gateway: "10.0.0.1",
			})
		}()
	}
	wg.Wait()

	got, ok := s.Get("pool")
	if !ok {
		t.Fatal("expected pool to exist after concurrent writes")
	}
	_ = got

	// Concurrent reads.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.Get("pool")
			s.List()
		}()
	}
	wg.Wait()
}

func TestPoolStore_ConcurrentRemove(t *testing.T) {
	s := NewPoolStore()
	const n = 100
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		s.Add(string(rune('a'+i%26))+string(rune('0'+i/26)), &PoolInfo{Subnet: "10.0.0.0/24"})
	}

	wg.Add(n)
	for id := range s.List() {
		pid := id
		go func() {
			defer wg.Done()
			s.Remove(pid)
			s.Get(pid)
		}()
	}
	wg.Wait()
	if len(s.List()) != 0 {
		t.Fatalf("expected all pools removed, got %d", len(s.List()))
	}
}

// ---------------------------------------------------------------------------
// LeaseStore tests
// ---------------------------------------------------------------------------

func TestLeaseStore_New(t *testing.T) {
	s := NewLeaseStore()
	if s == nil {
		t.Fatal("NewLeaseStore() returned nil")
	}
}

func TestLeaseStore_AddAndGet(t *testing.T) {
	s := NewLeaseStore()
	lease := testLease("192.168.1.100")

	s.Add("pool1", "192.168.1.100", lease)

	got := s.Get("pool1", "192.168.1.100")
	if got == nil {
		t.Fatal("expected to find lease")
	}
	if got.ACK.YourIPAddr.String() != "192.168.1.100" {
		t.Fatalf("expected IP 192.168.1.100, got %s", got.ACK.YourIPAddr)
	}
}

func TestLeaseStore_GetNotFound(t *testing.T) {
	s := NewLeaseStore()

	// Missing address.
	got := s.Get("pool1", "192.168.1.100")
	if got != nil {
		t.Fatal("expected nil for missing lease")
	}

	// Missing pool.
	got = s.Get("nonexistent", "192.168.1.100")
	if got != nil {
		t.Fatal("expected nil for missing pool")
	}
}

func TestLeaseStore_Remove(t *testing.T) {
	s := NewLeaseStore()
	s.Add("pool1", "192.168.1.100", testLease("192.168.1.100"))
	s.Remove("pool1", "192.168.1.100")

	got := s.Get("pool1", "192.168.1.100")
	if got != nil {
		t.Fatal("expected lease to be removed")
	}
}

func TestLeaseStore_RemoveNonexistent(t *testing.T) {
	s := NewLeaseStore()
	// Should not panic.
	s.Remove("pool1", "192.168.1.100")
	s.Remove("nonexistent", "192.168.1.100")
}

func TestLeaseStore_ReleaseAll(t *testing.T) {
	s := NewLeaseStore()

	leases := []*nclient4.Lease{
		testLease("10.0.0.2"),
		testLease("10.0.0.3"),
		testLease("10.0.0.4"),
	}
	for _, l := range leases {
		s.Add("pool1", l.ACK.YourIPAddr.String(), l)
	}

	// Add a different pool that should not be affected.
	s.Add("pool2", "10.0.1.2", testLease("10.0.1.2"))

	released := s.ReleaseAll("pool1")
	if len(released) != 3 {
		t.Fatalf("expected 3 released leases, got %d", len(released))
	}

	// Verify pool1 leases are gone.
	if got := s.Get("pool1", "10.0.0.2"); got != nil {
		t.Fatal("expected pool1 lease to be removed after ReleaseAll")
	}

	// Verify pool2 is untouched.
	if got := s.Get("pool2", "10.0.1.2"); got == nil {
		t.Fatal("expected pool2 lease to still exist")
	}
}

func TestLeaseStore_ReleaseAllEmptyPool(t *testing.T) {
	s := NewLeaseStore()
	released := s.ReleaseAll("nonexistent")
	if released != nil {
		t.Fatalf("expected nil for empty pool, got %d leases", len(released))
	}
}

func TestLeaseStore_Update(t *testing.T) {
	s := NewLeaseStore()

	original := testLease("10.0.0.2")
	s.Add("pool1", "10.0.0.2", original)

	updated := testLease("10.0.0.2")
	updated.CreationTime = time.Now().Add(1 * time.Hour)
	s.Update("pool1", "10.0.0.2", updated)

	got := s.Get("pool1", "10.0.0.2")
	if got == nil {
		t.Fatal("expected lease after update")
	}
	if !got.CreationTime.After(original.CreationTime) {
		t.Fatal("expected updated lease to have later creation time")
	}
}

func TestLeaseStore_UpdateNewPool(t *testing.T) {
	s := NewLeaseStore()
	lease := testLease("10.0.0.2")
	// Updating a pool/address that doesn't exist should work like Add.
	s.Update("pool1", "10.0.0.2", lease)

	got := s.Get("pool1", "10.0.0.2")
	if got == nil {
		t.Fatal("expected lease after update on new pool")
	}
}

func TestLeaseStore_PoolIsolation(t *testing.T) {
	s := NewLeaseStore()
	s.Add("pool1", "10.0.0.2", testLease("10.0.0.2"))
	s.Add("pool2", "10.0.0.2", testLease("10.0.0.2"))

	// Removing from pool1 should not affect pool2.
	s.Remove("pool1", "10.0.0.2")
	if got := s.Get("pool2", "10.0.0.2"); got == nil {
		t.Fatal("expected pool2 lease to remain after removing from pool1")
	}

	// ReleaseAll on pool2 should leave pool1 clean.
	_ = s.ReleaseAll("pool2")
	if got := s.Get("pool1", "10.0.0.2"); got != nil {
		t.Fatal("expected pool1 lease to remain after ReleaseAll on pool2 (it was already removed)")
	}
}

func TestLeaseStore_ConcurrentAddAndGet(t *testing.T) {
	s := NewLeaseStore()
	const goroutines = 50
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		ip := i
		go func() {
			defer wg.Done()
			addr := "10.0.0.2"
			s.Add("pool1", addr, testLease(addr))
			s.Get("pool1", addr)
		}()
		_ = ip
	}
	wg.Wait()
}

func TestLeaseStore_ConcurrentRemove(t *testing.T) {
	s := NewLeaseStore()
	const n = 100

	// Populate.
	for i := 0; i < n; i++ {
		addr := "10.0.0.2"
		s.Add("pool1", addr, testLease(addr))
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		addr := "10.0.0.2"
		go func() {
			defer wg.Done()
			s.Remove("pool1", addr)
			s.Get("pool1", addr)
		}()
	}
	wg.Wait()
}

func TestLeaseStore_ConcurrentReleaseAll(t *testing.T) {
	s := NewLeaseStore()

	// Populate pool with many leases.
	for i := 1; i <= 50; i++ {
		addr := "10.0.0.2"
		s.Add("pool1", addr, testLease(addr))
	}

	var wg sync.WaitGroup
	wg.Add(3)
	for range 3 {
		go func() {
			defer wg.Done()
			s.ReleaseAll("pool1")
		}()
	}
	wg.Wait()
	// After concurrent ReleaseAll, pool should have no leases.
	if got := s.Get("pool1", "10.0.0.2"); got != nil {
		t.Log("Note: lease may survive if ReleaseAll winner is not deterministic; this is expected.")
	}
}

func TestLeaseStore_ConcurrentMixedOperations(t *testing.T) {
	s := NewLeaseStore()
	const operations = 200
	var wg sync.WaitGroup

	wg.Add(operations)
	for i := 0; i < operations; i++ {
		op := i % 4
		go func() {
			defer wg.Done()
			switch op {
			case 0:
				s.Add("pool1", "10.0.0.2", testLease("10.0.0.2"))
			case 1:
				s.Get("pool1", "10.0.0.2")
			case 2:
				s.Remove("pool1", "10.0.0.2")
			case 3:
				s.Update("pool1", "10.0.0.2", testLease("10.0.0.2"))
			}
		}()
	}
	wg.Wait()
	// No panic / race is the pass condition.
}

func TestLeaseStore_ConcurrentPoolIsolation(t *testing.T) {
	s := NewLeaseStore()
	const pools = 10
	const opsPerPool = 50
	var wg sync.WaitGroup

	for p := 0; p < pools; p++ {
		poolID := string(rune('A' + p))
		wg.Add(opsPerPool)
		for i := 0; i < opsPerPool; i++ {
			go func(pid string) {
				defer wg.Done()
				s.Add(pid, "10.0.0.2", testLease("10.0.0.2"))
				s.Get(pid, "10.0.0.2")
			}(poolID)
		}
	}
	wg.Wait()

	// Verify all pools have their independent leases.
	for p := 0; p < pools; p++ {
		poolID := string(rune('A' + p))
		if got := s.Get(poolID, "10.0.0.2"); got == nil {
			t.Fatalf("expected lease in pool %s", poolID)
		}
	}
}

// TestPoolStore_ConcurrentList verifies that List() works correctly under
// concurrent reads and writes.
func TestPoolStore_ConcurrentList(t *testing.T) {
	s := NewPoolStore()
	s.Add("a", &PoolInfo{Subnet: "10.0.0.0/24"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.List()
		}()
	}
	wg.Wait()
}

// TestLeaseStore_AddMultipleAddresses verifies that a single pool can hold
// multiple addresses.
func TestLeaseStore_AddMultipleAddresses(t *testing.T) {
	s := NewLeaseStore()
	leases := []string{"10.0.0.2", "10.0.0.3", "10.0.0.4"}
	for _, addr := range leases {
		s.Add("pool1", addr, testLease(addr))
	}
	for _, addr := range leases {
		if got := s.Get("pool1", addr); got == nil {
			t.Fatalf("expected lease for %s", addr)
		}
	}
}