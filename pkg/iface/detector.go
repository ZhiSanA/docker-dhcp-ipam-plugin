// Package iface detects the host's primary network interface and extracts
// subnet, gateway, and MAC information for use by the DHCP IPAM plugin.
package iface

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
)

// Info holds network information for the detected host interface.
type Info struct {
	InterfaceName string
	HardwareAddr  net.HardwareAddr // MAC of the host interface
	Subnet        string           // CIDR notation like "192.168.1.0/24"
	Gateway       string           // Default gateway IP like "192.168.1.1"
	IP            net.IP           // Current IPv4 address on this interface
	IPNet         *net.IPNet       // The IP network (IP + Mask) from Addrs()
}

// Detect identifies the host's primary network interface. If ifaceName is
// non-empty, that interface is used. Otherwise, the default route interface
// is auto-detected by reading /proc/net/route.
//
// Returns the interface info or an error if detection fails.
func Detect(ifaceName string) (*Info, error) {
	return detectWithRouteFile("/proc/net/route", ifaceName)
}

// detectWithRouteFile is the testable inner implementation that accepts
// a path to the route file (real /proc/net/route or a temp file in tests).
func detectWithRouteFile(routeFile, ifaceName string) (*Info, error) {
	// 1. Parse /proc/net/route to find the default route and its gateway.
	routeIface, gatewayHex, err := parseDefaultRoute(routeFile)
	if err != nil {
		return nil, err
	}

	// 2. Determine which interface to use.
	useIface := ifaceName
	if useIface == "" {
		useIface = routeIface
	}

	// 3. Resolve the interface.
	iface, err := resolveInterface(useIface)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve interface %q: %w", useIface, err)
	}

	// 4. Find the first non-loopback IPv4 address.
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get addresses for interface %q: %w", iface.Name, err)
	}

	var ipNet *net.IPNet
	for _, addr := range addrs {
		ipNet, err = toIPv4Net(addr)
		if err != nil {
			continue
		}
		break
	}
	if ipNet == nil {
		return nil, fmt.Errorf("interface %q has no IPv4 address", iface.Name)
	}

	// 5. Compute subnet CIDR.
	ones, _ := ipNet.Mask.Size()
	network := ipNet.IP.Mask(ipNet.Mask)
	subnet := fmt.Sprintf("%s/%d", network.String(), ones)

	// 6. Convert gateway hex to IP string.
	gateway := parseGatewayHex(gatewayHex)

	return &Info{
		InterfaceName: iface.Name,
		HardwareAddr:  iface.HardwareAddr,
		Subnet:        subnet,
		Gateway:       gateway,
		IP:            ipNet.IP,
		IPNet:         ipNet,
	}, nil
}

// parseDefaultRoute reads the given route file and returns the interface
// name and gateway hex string for the default route (Destination == "00000000").
func parseDefaultRoute(routeFile string) (ifaceName, gatewayHex string, err error) {
	f, err := os.Open(routeFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to open route file %q: %w", routeFile, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip header line.
		if strings.HasPrefix(line, "Iface") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}

		if fields[1] == "00000000" {
			return fields[0], fields[2], nil
		}
	}

	return "", "", fmt.Errorf("no default route found in %q", routeFile)
}

// resolveInterface takes an interface name or numeric index (as a string)
// and returns the corresponding net.Interface.
func resolveInterface(name string) (*net.Interface, error) {
	// Try by name first (covers both modern kernels where the Iface column
	// is the name, and user-specified names).
	if iface, err := net.InterfaceByName(name); err == nil {
		return iface, nil
	}

	// Fall back to treating the string as a numeric index (older kernels
	// where /proc/net/route prints the interface index).
	var idx int
	if _, err := fmt.Sscanf(name, "%d", &idx); err == nil {
		if iface, err := net.InterfaceByIndex(idx); err == nil {
			return iface, nil
		}
	}

	return nil, fmt.Errorf("interface %q not found", name)
}

// toIPv4Net checks whether a net.Addr from Interface.Addrs() is an IPv4
// *net.IPNet and returns it. Non-IPv4 and loopback addresses are rejected.
func toIPv4Net(addr net.Addr) (*net.IPNet, error) {
	ipNet, ok := addr.(*net.IPNet)
	if !ok {
		return nil, fmt.Errorf("not an IPNet")
	}
	if ipNet.IP.To4() == nil {
		return nil, fmt.Errorf("not IPv4")
	}
	if ipNet.IP.IsLoopback() {
		return nil, fmt.Errorf("loopback")
	}
	return ipNet, nil
}

// parseGatewayHex converts a little-endian hex gateway string from
// /proc/net/route to a dotted-decimal IP string.
//
// Example: "010011AC" -> [0x01, 0x00, 0x11, 0xAC] (LE) ->
//
//	0xAC 0x11 0x00 0x01 -> "172.17.0.1"
func parseGatewayHex(h string) string {
	if len(h) < 8 {
		return ""
	}

	b, err := hex.DecodeString(h[:8])
	if err != nil || len(b) < 4 {
		return ""
	}

	return net.IPv4(b[3], b[2], b[1], b[0]).String()
}
