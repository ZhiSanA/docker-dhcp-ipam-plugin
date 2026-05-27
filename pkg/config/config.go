// Package config provides configuration loading for the Docker DHCP IPAM plugin.
//
// Configuration is read from environment variables with sensible defaults
// for all fields. The primary entry point is Load(), which returns a fully
// populated Config struct.
package config

import (
	"os"
	"strconv"
	"time"
)

// EnvVar names used by the plugin.
const (
	EnvInterface      = "DHCP_IPAM_INTERFACE"
	EnvSocketPath     = "DHCP_IPAM_SOCKET_PATH"
	EnvLogLevel       = "DHCP_IPAM_LOG_LEVEL"
	EnvRenewInterval  = "DHCP_IPAM_RENEW_INTERVAL"
	EnvDHCPTimeout    = "DHCP_IPAM_TIMEOUT"
	EnvDHCPRetries    = "DHCP_IPAM_RETRIES"
)

// Default values.
const (
	DefaultSocketPath        = "/run/docker/plugins/dhcp_ipam.sock"
	DefaultLogLevel          = "info"
	DefaultLeaseRenewInterval = 30 * time.Second
	DefaultDHCPTimeout       = 10 * time.Second
	DefaultDHCPRetries       = 3
)

// Config holds all configuration for the DHCP IPAM plugin.
type Config struct {
	// HostInterface is the network interface to use for DHCP.
	// When empty the plugin auto-detects the default route interface.
	HostInterface string

	// SocketPath is the Unix socket path the plugin listens on.
	SocketPath string

	// LogLevel controls the logging verbosity (debug, info, warn, error).
	LogLevel string

	// LeaseRenewInterval controls how often the plugin checks and
	// attempts to renew DHCP leases.
	LeaseRenewInterval time.Duration

	// DHCPTimeout is the maximum time to wait for a DHCP response.
	DHCPTimeout time.Duration

	// DHCPRetries is the number of times to retry a DHCP request on failure.
	DHCPRetries int
}

// Load reads configuration from environment variables and returns a
// fully-populated Config. Missing or invalid variables fall back to
// documented defaults.
func Load() *Config {
	cfg := &Config{}
	cfg.LoadFromEnv()
	return cfg
}

// LoadFromEnv populates the receiver Config from environment variables.
// Any field whose environment variable is unset or unparseable retains
// its current value (callers should ensure defaults are set beforehand).
func (c *Config) LoadFromEnv() {
	if v := os.Getenv(EnvInterface); v != "" {
		c.HostInterface = v
	}

	if v := os.Getenv(EnvSocketPath); v != "" {
		c.SocketPath = v
	} else if c.SocketPath == "" {
		c.SocketPath = DefaultSocketPath
	}

	if v := os.Getenv(EnvLogLevel); v != "" {
		c.LogLevel = v
	} else if c.LogLevel == "" {
		c.LogLevel = DefaultLogLevel
	}

	if v := os.Getenv(EnvRenewInterval); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.LeaseRenewInterval = d
		}
	} else if c.LeaseRenewInterval == 0 {
		c.LeaseRenewInterval = DefaultLeaseRenewInterval
	}

	if v := os.Getenv(EnvDHCPTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.DHCPTimeout = d
		}
	} else if c.DHCPTimeout == 0 {
		c.DHCPTimeout = DefaultDHCPTimeout
	}

	if v := os.Getenv(EnvDHCPRetries); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.DHCPRetries = n
		}
	} else if c.DHCPRetries == 0 {
		c.DHCPRetries = DefaultDHCPRetries
	}
}
