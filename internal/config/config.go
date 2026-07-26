package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const FileName = ".isola.toml"

// Config represents the .isola.toml configuration file.
type Config struct {
	// Project is this repo's machine-wide identity, used to namespace shared
	// proxy routing (<branch>.<project>.localhost) and Redis ownership. Unset
	// defaults to the main worktree's directory basename (slugified). It must be
	// a DNS label since it appears in a subdomain. See docs/adr/006-shared-proxy.md.
	Project   string                   `toml:"project"`
	Services  map[string]ServiceConfig `toml:"services"`
	Worktrees map[string]WTOverride    `toml:"worktrees"`
	// Accessories holds each [accessories.<name>] body as an undecoded TOML
	// primitive. The shared "kind" field is read centrally; the rest is decoded
	// lazily by the matching driver (see internal/accessory), so adding a kind
	// touches no code here. See docs/adr/005-accessories.md.
	Accessories map[string]toml.Primitive `toml:"accessories"`
	// Meta is the TOML metadata retained so drivers can PrimitiveDecode their
	// own fields out of the primitives above.
	Meta toml.MetaData `toml:"-"`
	// Proxy configures the reverse proxy that `isola up` auto-starts.
	Proxy ProxyConfig `toml:"proxy"`
	// CopyFiles lists glob patterns for gitignored files copied from the main
	// worktree into each worktree on `isola up` (git worktrees omit gitignored
	// files). Unset means the default [".env"]; an explicit empty list disables.
	CopyFiles []string `toml:"copy_files"`
	// Setup is a repo-root provisioning command run once per worktree on each
	// `isola up`, at the worktree root, after accessories are provisioned and
	// before any service's setup or command. Unlike per-service `setup` it
	// belongs to the worktree as a whole (e.g. a root `pnpm install` whose
	// `prepare` script installs git hooks, or generating a shared client). Keep
	// it idempotent; a non-zero exit aborts `up` before services start.
	Setup string `toml:"setup,omitempty"`
	// EnvFile controls whether isola also writes each service's resolved env
	// into an env file the app reads (in addition to the process environment).
	EnvFile EnvFileConfig `toml:"env_file"`
}

// EnvFileConfig is the project-wide policy for writing services' env into files.
type EnvFileConfig struct {
	// Enabled toggles writing env into services' env files. Unset means enabled.
	Enabled *bool `toml:"enabled"`
	// Create writes the file when absent; unset/false only updates an existing one.
	Create bool `toml:"create"`
	// Path is the default env-file name, resolved relative to each service's dir.
	// Unset means ".env". A service may override it with its own `env_file`.
	Path string `toml:"path"`
}

// boolOr returns *p, or def when p is nil. It centralizes the "unset means
// default" convention used by the optional *bool config toggles below.
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Scheme returns "https" when https is true, else "http". Shared by everything
// that builds a proxy URL from the proxy's HTTPS setting.
func Scheme(https bool) string {
	if https {
		return "https"
	}
	return "http"
}

// envFileEnabled reports whether env-file writing is on (default: on).
func (c *Config) envFileEnabled() bool {
	return boolOr(c.EnvFile.Enabled, true)
}

// ServiceEnvFile returns the env-file name for a service (relative to its dir),
// or "" if writing is disabled for it. Resolution: env-file writing off -> "";
// per-service `env_file` unset -> the global path (default ".env"); set to ""
// -> disabled for this service; set to a name -> that name.
func (c *Config) ServiceEnvFile(service string) string {
	if !c.envFileEnabled() {
		return ""
	}
	def := c.EnvFile.Path
	if def == "" {
		def = ".env"
	}
	svc, ok := c.Services[service]
	if !ok || svc.EnvFile == nil {
		return def
	}
	return *svc.EnvFile // may be "" (disabled) or a custom name
}

// FilesToCopy returns the glob patterns of files to copy into each worktree.
// Unset defaults to [".env"]; an explicit `copy_files = []` disables copying.
func (c *Config) FilesToCopy() []string {
	if c.CopyFiles == nil {
		return []string{".env"}
	}
	return c.CopyFiles
}

// ProxyConfig controls the reverse proxy `isola up` starts automatically.
type ProxyConfig struct {
	// Enabled toggles auto-starting the proxy on `isola up`. Unset means enabled;
	// set `enabled = false` to opt out and manage the proxy yourself.
	Enabled *bool `toml:"enabled"`
	// HTTPS makes the auto-started proxy serve HTTPS with auto-generated certs.
	HTTPS bool `toml:"https"`
	// AutoTrust toggles installing isola's CA into the system trust store on the
	// first HTTPS `up` (interactive terminals only). Unset means enabled; set
	// `auto_trust = false` to keep trust a manual `isola trust` step.
	AutoTrust *bool `toml:"auto_trust"`
}

// AutoProxyEnabled reports whether `isola up` should auto-start the proxy.
func (c *Config) AutoProxyEnabled() bool {
	return boolOr(c.Proxy.Enabled, true)
}

// AutoTrustEnabled reports whether `isola up` may auto-install the HTTPS CA into
// the system trust store on an interactive first run.
func (c *Config) AutoTrustEnabled() bool {
	return boolOr(c.Proxy.AutoTrust, true)
}

// ProjectName returns the configured project, or a slugified basename of
// stateRoot when unset. All worktrees of a repo share the same stateRoot, so the
// default is stable across worktrees.
func (c *Config) ProjectName(stateRoot string) string {
	if c.Project != "" {
		return c.Project
	}
	if s := slugifyLabel(filepath.Base(stateRoot)); s != "" {
		return s
	}
	return "project"
}

// slugifyLabel lowercases s and replaces runs of non-alphanumeric characters
// with a single hyphen, trimming leading/trailing hyphens, so a directory name
// becomes a valid DNS label.
func slugifyLabel(s string) string {
	var b []byte
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, byte(r))
			prevHyphen = false
		} else if !prevHyphen {
			b = append(b, '-')
			prevHyphen = true
		}
	}
	return strings.Trim(string(b), "-")
}

// isValidLabel reports whether s is a legal DNS label (lowercase alphanumeric
// and hyphens, not starting or ending with a hyphen, at most 63 bytes).
func isValidLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return false
		}
	}
	return true
}

// ProxyPorts returns the distinct proxy_ports across all configured services.
func (c *Config) ProxyPorts() []int {
	seen := map[int]bool{}
	var ports []int
	for _, svc := range c.Services {
		if svc.ProxyPort <= 0 {
			continue // background process: no proxy route
		}
		if !seen[svc.ProxyPort] {
			seen[svc.ProxyPort] = true
			ports = append(ports, svc.ProxyPort)
		}
	}
	return ports
}

// AccessoryKind reads the "kind" discriminator from an accessory body without
// decoding driver-specific fields.
func (c *Config) AccessoryKind(prim toml.Primitive) (string, error) {
	var disc struct {
		Kind string `toml:"kind"`
	}
	if err := c.Meta.PrimitiveDecode(prim, &disc); err != nil {
		return "", err
	}
	return disc.Kind, nil
}

// ServiceConfig defines a single service within a worktree.
type ServiceConfig struct {
	Command string `toml:"command"`
	// Setup runs once before Command on each `isola up`, in the service's Dir
	// with its full resolved env (accessory URLs included). Use it for install
	// or migration steps a fresh worktree needs (e.g. "npm install"). Make it
	// idempotent; if it fails, the service is not started.
	Setup     string            `toml:"setup,omitempty"`
	Dir       string            `toml:"dir"`
	PortRange PortRange         `toml:"port_range"`
	ProxyPort int               `toml:"proxy_port"`
	Env       map[string]string `toml:"env,omitempty"`
	// EnvFile overrides the env-file name for this service (relative to its dir).
	// Unset inherits [env_file].path; "" opts this service out. See EnvFileConfig.
	EnvFile *string `toml:"env_file,omitempty"`
}

// PortRange defines the range of ports available for allocation.
type PortRange struct {
	Min int `toml:"min"`
	Max int `toml:"max"`
}

// WTOverride defines per-worktree overrides.
type WTOverride struct {
	Services map[string]WTServiceOverride `toml:"services"`
}

// WTServiceOverride defines per-worktree per-service overrides.
type WTServiceOverride struct {
	Command string            `toml:"command,omitempty"`
	Port    int               `toml:"port,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
}

// Load reads and parses the config file from the given repo root.
func Load(repoRoot string) (*Config, error) {
	path := filepath.Join(repoRoot, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s not found in %s; run 'isola init' first", FileName, repoRoot)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", FileName, err)
	}
	cfg.Meta = md

	if cfg.Services == nil {
		cfg.Services = map[string]ServiceConfig{}
	}
	if cfg.Worktrees == nil {
		cfg.Worktrees = map[string]WTOverride{}
	}
	if cfg.Accessories == nil {
		cfg.Accessories = map[string]toml.Primitive{}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if len(c.Services) == 0 {
		return fmt.Errorf("at least one service must be defined in [services]")
	}

	if c.Project != "" && !isValidLabel(c.Project) {
		return fmt.Errorf("project %q must be a DNS label: lowercase letters, digits, and hyphens, not starting or ending with a hyphen", c.Project)
	}

	proxyPorts := make(map[int]string)
	for name, svc := range c.Services {
		if svc.Command == "" {
			return fmt.Errorf("service %q: command must not be empty", name)
		}
		// A service with neither port_range nor proxy_port is a background
		// process (a worker, a queue consumer): isola runs and manages it with
		// env injection, but allocates no $PORT and adds no proxy route or URL.
		hasRange := svc.PortRange.Min > 0 || svc.PortRange.Max > 0
		if hasRange {
			if svc.PortRange.Min <= 0 || svc.PortRange.Max <= 0 {
				return fmt.Errorf("service %q: port_range needs both a positive min and max", name)
			}
			if svc.PortRange.Min > svc.PortRange.Max {
				return fmt.Errorf("service %q: port_range.min (%d) must be <= port_range.max (%d)",
					name, svc.PortRange.Min, svc.PortRange.Max)
			}
		}
		if svc.ProxyPort > 0 {
			if !hasRange {
				return fmt.Errorf("service %q: proxy_port needs a port_range (the backend port to route to); omit both for a background process", name)
			}
			if existing, ok := proxyPorts[svc.ProxyPort]; ok {
				return fmt.Errorf("services %q and %q have the same proxy_port %d", existing, name, svc.ProxyPort)
			}
			proxyPorts[svc.ProxyPort] = name
		}
	}

	// Validate per-worktree port overrides are within range
	for wtName, wt := range c.Worktrees {
		for svcName, svcOverride := range wt.Services {
			svc, ok := c.Services[svcName]
			if !ok {
				return fmt.Errorf("worktree %q references unknown service %q", wtName, svcName)
			}
			if svcOverride.Port != 0 && (svcOverride.Port < svc.PortRange.Min || svcOverride.Port > svc.PortRange.Max) {
				return fmt.Errorf("worktree %q service %q port %d is outside range [%d, %d]",
					wtName, svcName, svcOverride.Port, svc.PortRange.Min, svc.PortRange.Max)
			}
		}
	}

	// Validate accessories declare a kind (driver-specific fields are checked
	// by the driver when it is built).
	for name, prim := range c.Accessories {
		kind, err := c.AccessoryKind(prim)
		if err != nil {
			return fmt.Errorf("accessory %q: %w", name, err)
		}
		if kind == "" {
			return fmt.Errorf("accessory %q: kind must not be empty", name)
		}
	}

	// Check for port range overlaps between services.
	svcNames := make([]string, 0, len(c.Services))
	for name := range c.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)
	for i := 0; i < len(svcNames); i++ {
		for j := i + 1; j < len(svcNames); j++ {
			a := c.Services[svcNames[i]]
			b := c.Services[svcNames[j]]
			// Background processes have no port_range; they can't overlap.
			if a.PortRange.Max <= 0 || b.PortRange.Max <= 0 {
				continue
			}
			if a.PortRange.Min <= b.PortRange.Max && b.PortRange.Min <= a.PortRange.Max {
				return fmt.Errorf("services %q and %q have overlapping port ranges [%d-%d] and [%d-%d]",
					svcNames[i], svcNames[j], a.PortRange.Min, a.PortRange.Max, b.PortRange.Min, b.PortRange.Max)
			}
		}
	}

	return nil
}

// Init creates a default .isola.toml file in the given directory.
func Init(dir string) (string, error) {
	path := filepath.Join(dir, FileName)
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("%s already exists", FileName)
	}

	content := `#:schema https://raw.githubusercontent.com/cyucelen/isola/main/isola.schema.tosd
# isola config. Docs: https://github.com/cyucelen/isola

# setup = "pnpm install"                # optional: repo-root step, runs once per
                                        # worktree on up, before any service

[services.frontend]
command = "pnpm run dev"
# setup = "pnpm install"              # optional: install/prep, runs before command
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000

[services.frontend.env]
API_URL = "${services.backend.url}"

[services.backend]
command = "go run ./cmd/server"
dir = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000

# Per-worktree database (optional):
# [accessories.database]
# kind       = "postgres"
# server_url = "postgres://USER:PASS@HOST:PORT/postgres"
# clone_from = "myapp_dev"
# name       = "myapp_${ISOLA_BRANCH_SLUG}"
#
# [services.backend.env]
# DATABASE_URL = "${accessories.database.url}"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("writing %s: %w", FileName, err)
	}
	return path, nil
}

// CommandForBranch returns the command for a given service and branch,
// checking for per-worktree overrides.
func (c *Config) CommandForBranch(service, branch string) string {
	if wt, ok := c.Worktrees[branch]; ok {
		if svc, ok := wt.Services[service]; ok && svc.Command != "" {
			return svc.Command
		}
	}
	return c.Services[service].Command
}

// EnvForBranch returns merged environment variables for a given service and branch.
// Priority (low -> high): [services.<svc>].env -> [worktrees.<branch>].services.<svc>.env
func (c *Config) EnvForBranch(service, branch string) map[string]string {
	merged := map[string]string{}
	if svc, ok := c.Services[service]; ok {
		for k, v := range svc.Env {
			merged[k] = v
		}
	}
	if wt, ok := c.Worktrees[branch]; ok {
		if svc, ok := wt.Services[service]; ok {
			for k, v := range svc.Env {
				merged[k] = v
			}
		}
	}
	return merged
}

// FixedPortForBranch returns the fixed port for a branch+service, or 0 if none.
func (c *Config) FixedPortForBranch(service, branch string) int {
	if wt, ok := c.Worktrees[branch]; ok {
		if svc, ok := wt.Services[service]; ok {
			return svc.Port
		}
	}
	return 0
}
