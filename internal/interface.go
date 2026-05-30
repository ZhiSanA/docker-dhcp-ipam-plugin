package internal

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// ── Interface Info ────────────────────────────────────────────────────

type InterfaceInfo struct {
	Name      string
	MAC       net.HardwareAddr
	SubnetV4  string // CIDR, e.g. "192.168.1.0/24"
	GatewayV4 string
	IPV4      net.IP
	SubnetV6  string // CIDR, e.g. "2001:db8::/64"
	GatewayV6 string
	IPV6      net.IP
}

// Subnet returns the IPv4 subnet (backwards compatibility).
func (i *InterfaceInfo) Subnet() string { return i.SubnetV4 }

// Gateway returns the IPv4 gateway (backwards compatibility).
func (i *InterfaceInfo) Gateway() string { return i.GatewayV4 }

// ── Detection ─────────────────────────────────────────────────────────

func DetectInterface(ifaceName string) (*InterfaceInfo, error) {
	var name string
	if ifaceName != "" {
		name = ifaceName
	} else {
		var err error
		name, _, err = parseProcNetRoute()
		if err != nil {
			return nil, err
		}
	}
	return resolveInterface(name)
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

	info := &InterfaceInfo{
		Name: iface.Name,
		MAC:  iface.HardwareAddr,
	}

	// Detect IPv4 and IPv6 from interface addresses
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			if info.IPV4 != nil {
				continue
			}
			ones, _ := ipNet.Mask.Size()
			network := ip4.Mask(ipNet.Mask)
			info.IPV4 = ip4
			info.SubnetV4 = fmt.Sprintf("%s/%d", network.String(), ones)
		} else if ip6 := ipNet.IP.To16(); ip6 != nil {
			if info.IPV6 != nil {
				continue
			}
			ones, _ := ipNet.Mask.Size()
			network := ip6.Mask(ipNet.Mask)
			info.IPV6 = ip6
			info.SubnetV6 = fmt.Sprintf("%s/%d", network.String(), ones)
		}
	}
	if info.IPV4 == nil {
		return nil, fmt.Errorf("interface %q has no IPv4 address", name)
	}

	// Detect IPv4 gateway from default route
	_, gwHex, err := parseProcNetRoute()
	if err == nil && gwHex != "" {
		info.GatewayV4 = parseGatewayHex(gwHex)
	}

	// Detect IPv6 gateway from default route
	if gw6, err := parseProcNetIPv6Route(name); err == nil {
		info.GatewayV6 = gw6
	}

	return info, nil
}

// ── /proc/net/route parsing (IPv4) ────────────────────────────────────

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

// ── /proc/net/ipv6_route parsing ──────────────────────────────────────

func parseProcNetIPv6Route(ifaceName string) (string, error) {
	f, err := os.Open("/proc/net/ipv6_route")
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		if fields[9] != ifaceName {
			continue
		}
		// Destination "00000000000000000000000000000000" with prefix "00" = default
		if fields[0] == "00000000000000000000000000000000" && fields[1] == "00" {
			gw := fields[4]
			if gw == "00000000000000000000000000000000" {
				continue
			}
			return parseIPv6Hex(gw), nil
		}
	}
	return "", fmt.Errorf("no IPv6 default route found for %q", ifaceName)
}

func parseIPv6Hex(h string) string {
	if len(h) < 32 {
		return ""
	}
	var buf [16]byte
	for i := 0; i < 16; i++ {
		buf[i] = hexByte(h[i*2 : i*2+2])
	}
	return net.IP(buf[:]).String()
}

func hexByte(s string) byte {
	var v byte
	for i := 0; i < 2; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + (c - '0')
		case c >= 'a' && c <= 'f':
			v = v*16 + (c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			v = v*16 + (c - 'A' + 10)
		}
	}
	return v
}
