package internal

import (
	"os"
	"strconv"
	"time"
)

// ── Config ────────────────────────────────────────────────────────────

const (
	envInterface     = "DHCP_IPAM_INTERFACE"
	envSocketPath    = "DHCP_IPAM_SOCKET_PATH"
	envLogLevel      = "DHCP_IPAM_LOG_LEVEL"
	envRenewInterval = "DHCP_IPAM_RENEW_INTERVAL"
	envDHCPTimeout   = "DHCP_IPAM_TIMEOUT"
	envDHCPRetries   = "DHCP_IPAM_RETRIES"
	envMACFromName   = "DHCP_IPAM_MAC_FROM_NAME"

	defaultSocketPath = "/run/docker/plugins/dhcp-ipam.sock"
	defaultLogLevel   = "info"
)

type Config struct {
	HostInterface      string
	SocketPath         string
	LogLevel           string
	LeaseRenewInterval time.Duration
	DHCPTimeout        time.Duration
	DHCPRetries        int
	MACFromName        bool
}

func LoadConfig() *Config {
	cfg := &Config{
		SocketPath:         defaultSocketPath,
		LogLevel:           defaultLogLevel,
		LeaseRenewInterval: 30 * time.Second,
		DHCPTimeout:        10 * time.Second,
		DHCPRetries:        3,
		MACFromName:        true,
	}
	if v := os.Getenv(envInterface); v != "" {
		cfg.HostInterface = v
	}
	if v := os.Getenv(envSocketPath); v != "" {
		cfg.SocketPath = v
	}
	if v := os.Getenv(envRenewInterval); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.LeaseRenewInterval = d
		}
	}
	if v := os.Getenv(envDHCPTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DHCPTimeout = d
		}
	}
	if v := os.Getenv(envDHCPRetries); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DHCPRetries = n
		}
	}
	if v := os.Getenv(envMACFromName); v != "" {
		cfg.MACFromName = v == "1" || v == "true"
	}
	return cfg
}
