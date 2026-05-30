package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// ── Interface Info ────────────────────────────────────────────────────

type InterfaceInfo struct {
	Name    string
	MAC     net.HardwareAddr
	Subnet  string // CIDR, e.g. "192.168.1.0/24"
	Gateway string
	IP      net.IP
}

// ── Detection ─────────────────────────────────────────────────────────

func detectInterface(ifaceName string) (*InterfaceInfo, error) {
	if ifaceName != "" {
		return detectNamedInterface(ifaceName)
	}
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
