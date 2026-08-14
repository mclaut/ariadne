// Package qdrantauth centralizes Qdrant client authentication and remote
// transport policy for the MCP server, hooks, import, ariadnectl and installer.
package qdrantauth

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
)

const (
	EnvAPIKey              = "ARIADNE_QDRANT_API_KEY"      //nolint:gosec // environment variable name, not a credential
	EnvAPIKeyFile          = "ARIADNE_QDRANT_API_KEY_FILE" //nolint:gosec // environment variable name, not a credential
	EnvTLS                 = "ARIADNE_QDRANT_TLS"
	EnvAllowInsecureRemote = "ARIADNE_QDRANT_ALLOW_INSECURE_REMOTE"
	maxKeyBytes            = 16 * 1024
)

// Config contains the resolved secret only in process memory. Callers must
// never print, serialize or persist APIKey.
type Config struct {
	APIKey              string
	APIKeyFile          string
	UseTLS              bool
	AllowInsecureRemote bool
}

// FromEnv resolves the optional API key and transport policy. A key file is
// preferable for long-running client registrations because configs can retain
// only its path instead of the secret value.
func FromEnv() (Config, error) {
	return FromValues(
		os.Getenv(EnvAPIKey),
		os.Getenv(EnvAPIKeyFile),
		os.Getenv(EnvTLS),
		os.Getenv(EnvAllowInsecureRemote),
	)
}

// FromValues is the testable form of FromEnv.
func FromValues(rawKey, keyFile, rawTLS, rawAllowInsecure string) (Config, error) {
	var cfg Config
	rawKey = strings.TrimSpace(rawKey)
	keyFile = strings.TrimSpace(keyFile)
	if rawKey != "" && keyFile != "" {
		return cfg, fmt.Errorf("set only one of %s or %s", EnvAPIKey, EnvAPIKeyFile)
	}
	var err error
	if cfg.UseTLS, err = parseBool(EnvTLS, rawTLS); err != nil {
		return Config{}, err
	}
	if cfg.AllowInsecureRemote, err = parseBool(EnvAllowInsecureRemote, rawAllowInsecure); err != nil {
		return Config{}, err
	}
	if keyFile != "" {
		key, readErr := readKeyFile(keyFile)
		if readErr != nil {
			return Config{}, readErr
		}
		cfg.APIKey = key
		cfg.APIKeyFile = keyFile
		return cfg, nil
	}
	if err := validateKey(rawKey); err != nil {
		return Config{}, err
	}
	cfg.APIKey = rawKey
	return cfg, nil
}

func readKeyFile(path string) (string, error) {
	info, err := os.Stat(path) //nolint:gosec // exact credential path explicitly supplied by the user
	if err != nil {
		return "", fmt.Errorf("%s cannot be opened", EnvAPIKeyFile)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must name a regular file", EnvAPIKeyFile)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s permissions must not allow group or other access", EnvAPIKeyFile)
	}
	if info.Size() > maxKeyBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", EnvAPIKeyFile, maxKeyBytes)
	}
	body, err := os.ReadFile(path) //nolint:gosec // explicit credential path from the user
	if err != nil {
		return "", fmt.Errorf("%s cannot be read", EnvAPIKeyFile)
	}
	key := strings.TrimSpace(string(body))
	if key == "" {
		return "", fmt.Errorf("%s is empty", EnvAPIKeyFile)
	}
	if err := validateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func validateKey(key string) error {
	if strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("qdrant API key must be a single line")
	}
	return nil
}

func parseBool(name, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be 0/1, true/false, yes/no, or on/off", name)
	}
}

// IsLoopbackHost deliberately avoids DNS resolution: an arbitrary hostname
// that happens to resolve locally must still be treated as a remote boundary.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateGRPC rejects remote plaintext/no-auth connections unless the user
// explicitly opts into the insecure escape hatch. Loopback and SSH tunnels
// continue to work without authentication.
func (c Config) ValidateGRPC(host string) error {
	if IsLoopbackHost(host) || c.AllowInsecureRemote {
		return nil
	}
	if c.APIKey == "" {
		return fmt.Errorf("remote Qdrant requires %s or %s", EnvAPIKey, EnvAPIKeyFile)
	}
	if !c.UseTLS {
		return fmt.Errorf("remote Qdrant with an API key requires %s=1", EnvTLS)
	}
	return nil
}

// ValidateURL applies the same fail-closed policy to Qdrant REST endpoints.
func (c Config) ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid Qdrant REST URL")
	}
	if IsLoopbackHost(u.Hostname()) || c.AllowInsecureRemote {
		return nil
	}
	if c.APIKey == "" {
		return fmt.Errorf("remote Qdrant requires %s or %s", EnvAPIKey, EnvAPIKeyFile)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("remote Qdrant with an API key requires HTTPS")
	}
	return nil
}

// Apply adds the Qdrant authentication header without exposing the key to
// request URLs, logs or error strings.
func (c Config) Apply(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("api-key", c.APIKey)
	}
}
