package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ghConfigDir returns the gh config directory (~/.config/gh).
func ghConfigDir() string {
	if d := os.Getenv("GH_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gh")
}

// hostsFilePath returns the path to the gh hosts config file.
func hostsFilePath() string {
	return filepath.Join(ghConfigDir(), "hosts.yml")
}

// HostConfig represents the structure of a single host entry in hosts.yml.
type HostConfig struct {
	User        string                `yaml:"user,omitempty"`
	OAuthToken  string                `yaml:"oauth_token,omitempty"`
	GitProtocol string                `yaml:"git_protocol,omitempty"`
	Users       map[string]UserConfig `yaml:"users,omitempty"`
}

// UserConfig represents a single user under a host in hosts.yml.
type UserConfig struct {
	OAuthToken string `yaml:"oauth_token,omitempty"`
}

// HostsConfig represents the top-level hosts.yml structure.
type HostsConfig map[string]*HostConfig

// readHostsConfig reads and parses the gh hosts.yml file.
func readHostsConfig() (HostsConfig, error) {
	data, err := os.ReadFile(hostsFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return HostsConfig{}, nil
		}
		return nil, err
	}
	var cfg HostsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse hosts.yml: %w", err)
	}
	if cfg == nil {
		cfg = HostsConfig{}
	}
	return cfg, nil
}

// writeHostsConfig writes the hosts config back to hosts.yml.
func writeHostsConfig(cfg HostsConfig) error {
	dir := ghConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg) //nolint:gosec // Intentional: writing oauth_token to gh config file.
	if err != nil {
		return err
	}
	return os.WriteFile(hostsFilePath(), data, 0o600)
}

// saveToken stores a token in hosts.yml using the same layout as `gh auth login`.
// This writes the token in insecure (plain text) mode which is the common case
// in server environments without a keyring.
func saveToken(hostname, username, token, gitProtocol string) error {
	cfg, err := readHostsConfig()
	if err != nil {
		return err
	}

	host, ok := cfg[hostname]
	if !ok {
		host = &HostConfig{}
		cfg[hostname] = host
	}

	host.User = username
	host.OAuthToken = token
	if gitProtocol != "" {
		host.GitProtocol = gitProtocol
	}

	if host.Users == nil {
		host.Users = make(map[string]UserConfig)
	}
	host.Users[username] = UserConfig{OAuthToken: token}

	return writeHostsConfig(cfg)
}

// getStoredToken reads the active token for a host from hosts.yml.
func getStoredToken(hostname string) (token, username string, err error) {
	cfg, err := readHostsConfig()
	if err != nil {
		return "", "", err
	}

	host, ok := cfg[hostname]
	if !ok {
		return "", "", fmt.Errorf("no config for host %s", hostname)
	}

	return host.OAuthToken, host.User, nil
}

// removeHost removes a host entry from hosts.yml.
func removeHost(hostname string) error {
	cfg, err := readHostsConfig()
	if err != nil {
		return err
	}
	delete(cfg, hostname)
	return writeHostsConfig(cfg)
}
