package iface

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// writeRouteFile creates a temporary route file with the given content.
func writeRouteFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "route")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp route file: %v", err)
	}
	return path
}

// --- parseDefaultRoute tests -------------------------------------------------

func TestParseDefaultRoute(t *testing.T) {
	t.Run("finds default route", func(t *testing.T) {
		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	010011AC	0003	0	0	100	00000000	0	0	0
eth0	0000A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
`
		path := writeRouteFile(t, content)
		iface, gw, err := parseDefaultRoute(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if iface != "eth0" {
			t.Errorf("expected iface eth0, got %q", iface)
		}
		if gw != "010011AC" {
			t.Errorf("expected gateway 010011AC, got %q", gw)
		}
	})

	t.Run("returns error when no default route", func(t *testing.T) {
		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	0000A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
`
		path := writeRouteFile(t, content)
		_, _, err := parseDefaultRoute(path)
		if err == nil {
			t.Fatal("expected error for missing default route")
		}
	})

	t.Run("returns error for empty file", func(t *testing.T) {
		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
`
		path := writeRouteFile(t, content)
		_, _, err := parseDefaultRoute(path)
		if err == nil {
			t.Fatal("expected error for empty route file")
		}
	})

	t.Run("returns error for file not found", func(t *testing.T) {
		_, _, err := parseDefaultRoute("/nonexistent/route")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("ignores malformed lines", func(t *testing.T) {
		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	010011AC	0003
` // truncated line, fewer than 8 fields
		path := writeRouteFile(t, content)
		_, _, err := parseDefaultRoute(path)
		if err == nil {
			t.Fatal("expected error because truncated line should be skipped")
		}
	})
}

// --- parseGatewayHex tests ---------------------------------------------------

func TestParseGatewayHex(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want string
	}{
		{"standard gateway", "010011AC", "172.17.0.1"},
		{"all zeros", "00000000", "0.0.0.0"},
		{"classic gateway 192.168.1.1", "0101A8C0", "192.168.1.1"},
		{"10.0.0.1", "0100000A", "10.0.0.1"},
		{"short string", "FF", ""},
		{"empty string", "", ""},
		{"non-hex string", "ZZZZZZZZ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGatewayHex(tt.hex)
			if got != tt.want {
				t.Errorf("parseGatewayHex(%q) = %q, want %q", tt.hex, got, tt.want)
			}
		})
	}
}

// --- toIPv4Net tests ---------------------------------------------------------

func TestToIPv4Net(t *testing.T) {
	t.Run("accepts valid IPv4", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("192.168.1.100/24")
		result, err := toIPv4Net(ipNet)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IP.Equal(net.ParseIP("192.168.1.0")) {
			t.Errorf("unexpected IP: %s", result.IP)
		}
	})

	t.Run("rejects loopback", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("127.0.0.1/8")
		_, err := toIPv4Net(ipNet)
		if err == nil {
			t.Fatal("expected error for loopback")
		}
	})

	t.Run("rejects IPv6", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("fe80::1/64")
		_, err := toIPv4Net(ipNet)
		if err == nil {
			t.Fatal("expected error for IPv6")
		}
	})

	t.Run("rejects non-IPNet (e.g. IPAddr)", func(t *testing.T) {
		addr := &net.IPAddr{IP: net.ParseIP("10.0.0.1")}
		_, err := toIPv4Net(addr)
		if err == nil {
			t.Fatal("expected error for non-IPNet")
		}
	})
}

// --- detectWithRouteFile integration tests -----------------------------------

func TestDetectWithRouteFile(t *testing.T) {
	t.Run("success with default route on lo interface", func(t *testing.T) {
		// Find a real non-loopback IPv4 interface for a meaningful test.
		iface := findNonLoopbackIPv4Interface(t)
		if iface == nil {
			t.Skip("no non-loopback IPv4 interface available for integration test")
		}

		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
` + iface.Name + `	00000000	010011AC	0003	0	0	100	00000000	0	0	0
`
		path := writeRouteFile(t, content)
		info, err := detectWithRouteFile(path, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.InterfaceName != iface.Name {
			t.Errorf("expected interface %q, got %q", iface.Name, info.InterfaceName)
		}
		if info.HardwareAddr == nil || len(info.HardwareAddr) == 0 {
			t.Error("expected non-empty HardwareAddr")
		}
		if info.Subnet == "" {
			t.Error("expected non-empty Subnet")
		}
		if info.Gateway != "172.17.0.1" {
			t.Errorf("expected gateway 172.17.0.1, got %q", info.Gateway)
		}
		if info.IP == nil {
			t.Error("expected non-nil IP")
		}
		if info.IP.IsLoopback() {
			t.Error("expected non-loopback IP")
		}
	})

	t.Run("specified interface by name", func(t *testing.T) {
		iface := findNonLoopbackIPv4Interface(t)
		if iface == nil {
			t.Skip("no non-loopback IPv4 interface available for integration test")
		}

		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
lo	00000000	00000000	0003	0	0	100	00000000	0	0	0
`
		path := writeRouteFile(t, content)
		info, err := detectWithRouteFile(path, iface.Name)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.InterfaceName != iface.Name {
			t.Errorf("expected interface %q, got %q", iface.Name, info.InterfaceName)
		}
	})

	t.Run("error when specified interface does not exist", func(t *testing.T) {
		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	010011AC	0003	0	0	100	00000000	0	0	0
`
		path := writeRouteFile(t, content)
		_, err := detectWithRouteFile(path, "nonexistent99")
		if err == nil {
			t.Fatal("expected error for non-existent interface")
		}
	})

	t.Run("error when no default route in file", func(t *testing.T) {
		content := `Iface	Destination	Gateway		Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	0000A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
`
		path := writeRouteFile(t, content)
		_, err := detectWithRouteFile(path, "")
		if err == nil {
			t.Fatal("expected error for missing default route")
		}
	})

	t.Run("error when route file missing", func(t *testing.T) {
		_, err := detectWithRouteFile("/nonexistent/route/file", "")
		if err == nil {
			t.Fatal("expected error for missing route file")
		}
	})
}

// --- helpers -----------------------------------------------------------------

// findNonLoopbackIPv4Interface returns the first non-loopback interface
// with at least one IPv4 address, or nil if none is found.
func findNonLoopbackIPv4Interface(t *testing.T) *net.Interface {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("net.Interfaces() failed: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
				return &iface
			}
		}
	}
	return nil
}
