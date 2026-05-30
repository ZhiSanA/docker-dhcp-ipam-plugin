package internal

import (
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