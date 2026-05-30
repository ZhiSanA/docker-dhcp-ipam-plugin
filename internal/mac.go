package internal

import (
	"fmt"
	"hash/fnv"
	"log"
	"net"
)

// ── MAC Resolution ───────────────────────────────────────────────────

// resolveMAC picks the best MAC address to use for the DHCP request:
//
//  1. If MACFromName is enabled and the endpoint name is available,
//     generate a deterministic MAC from the name so that rebuilding a
//     container with the same name receives the same IP.
//  2. Otherwise, use the MAC passed by Docker via options.
//  3. Fall back to a hash of poolID (stable for the life of the driver).
func resolveMAC(opts map[string]string, poolID string, cfg *Config) net.HardwareAddr {
	if cfg.MACFromName {
		if name := opts["com.docker.network.endpoint.name"]; name != "" {
			return generateMAC(name)
		}
		log.Print("resolveMAC: MACFromName enabled but no endpoint name in options, falling back to options MAC")
	}

	for _, key := range []string{"com.docker.network.endpoint.macaddress", "mac", "macaddress"} {
		if v, ok := opts[key]; ok && v != "" {
			if hw, err := net.ParseMAC(v); err == nil {
				return hw
			}
		}
	}
	return generateMAC(poolID)
}

// generateMAC derives a stable MAC from an arbitrary string via FNV-32a.
// The resulting MAC has the locally-administered unicast prefix (02).
func generateMAC(s string) net.HardwareAddr {
	h := fnv.New32a()
	h.Write([]byte(s))
	sum := h.Sum32()
	return net.HardwareAddr{0x02, 0x1a, 0x2b, byte(sum >> 16), byte(sum >> 8), byte(sum)}
}

// eui64Address generates a stable IPv6 address from a subnet CIDR and a MAC
// using the EUI-64 format (insert FFFE in the middle of MAC, flip U/L bit).
// Returns a CIDR string like "2409:8a50:a70:2110:xx:xx:xx:xx/64".
func eui64Address(subnetCIDR string, mac net.HardwareAddr) string {
	_, ipNet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return subnetCIDR
	}

	// Build the interface identifier from the MAC using EUI-64
	// MAC:  XX:XX:XX:XX:XX:XX
	// EUI-64: XX:XX:XX:FF:FE:XX:XX:XX  (insert FF:FE in the middle)
	// Then flip the U/L bit (bit 1 of the first byte)
	ones, _ := ipNet.Mask.Size()
	prefix := ipNet.IP

	eui64 := make(net.IP, 16)
	copy(eui64, prefix[:8])  // first 8 bytes = subnet prefix (for /64)

	// Insert FF:FE and flip U/L bit
	eui64[8] = mac[0] ^ 0x02 // flip U/L bit
	eui64[9] = mac[1]
	eui64[10] = mac[2]
	eui64[11] = 0xFF
	eui64[12] = 0xFE
	eui64[13] = mac[3]
	eui64[14] = mac[4]
	eui64[15] = mac[5]

	return fmt.Sprintf("%s/%d", eui64.String(), ones)
}
