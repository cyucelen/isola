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

// EnvFileEnabled reports whether env-file writing is on (default: on).
func (c *Config) EnvFileEnabled() bool {
	return c.EnvFile.Enabled == nil || *c.EnvFile.Enabled
}

// ServiceEnvFile returns the env-file name for a service (relative to its dir),
// or "" if writing is disabled for it. Resolution: env-file writing off -> "";
// per-service `env_file` unset -> the global path (default ".env"); set to ""
// -> disabled for this service; set to a name -> that name.
func (c *Config) ServiceEnvFile(service string) string {
	if !c.EnvFileEnabled() {
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
	return c.Proxy.Enabled == nil || *c.Proxy.Enabled
}

// AutoTrustEnabled reports whether `isola up` may auto-install the HTTPS CA into
// the system trust store on an interactive first run.
func (c *Config) AutoTrustEnabled() bool {
	return c.Proxy.AutoTrust == nil || *c.Proxy.AutoTrust
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
	Command   string            `toml:"command"`
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

// DefaultConfig returns a default configuration with a single frontend service.
func DefaultConfig() *Config {
	return &Config{
		Services: map[string]ServiceConfig{
			"frontend": {
				Command: "npm run dev",
				Dir:     "",
				PortRange: PortRange{
					Min: 3100,
					Max: 3199,
				},
				ProxyPort: 3000,
			},
		},
		Worktrees: map[string]WTOverride{},
	}
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
		if svc.PortRange.Min <= 0 || svc.PortRange.Max <= 0 {
			return fmt.Errorf("service %q: port_range.min and port_range.max must be positive", name)
		}
		if svc.PortRange.Min > svc.PortRange.Max {
			return fmt.Errorf("service %q: port_range.min (%d) must be <= port_range.max (%d)",
				name, svc.PortRange.Min, svc.PortRange.Max)
		}
		if svc.ProxyPort <= 0 {
			return fmt.Errorf("service %q: proxy_port must be positive", name)
		}
		if existing, ok := proxyPorts[svc.ProxyPort]; ok {
			return fmt.Errorf("services %q and %q have the same proxy_port %d", existing, name, svc.ProxyPort)
		}
		proxyPorts[svc.ProxyPort] = name
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

	content := `# isola configuration - an isolated dev environment per git worktree
# See: https://github.com/cyucelen/isola

# --- Project name ---
# Namespaces this repo across the shared proxy (<branch>.<project>.localhost) and
# per-worktree Redis. Defaults to this repo's directory name; set it only to
# override or to resolve a clash with another repo of the same name.
# project = "myapp"

# --- Files copied into each worktree ---
# Gitignored files (absent from new worktrees) copied from the main worktree on
# 'isola up'. Existing files are never overwritten. Defaults to [".env"]; set to
# [] to disable. Must stay above the [sections] below (it is a top-level key).
# copy_files = [".env", ".env.*"]

# --- Env files (optional) ---
# Besides the process environment, isola can write each service's resolved env
# (your [env], accessory URLs, and any \${...} refs) into an env file the app
# reads. Resolved relative to each service's dir; a service can override the
# filename with its own env_file = "..." (or "" to opt out).
# [env_file]
# enabled = true    # write env into services' env files
# create  = false   # only update an existing file (true = create it if missing)
# path    = ".env"  # default filename, per service dir

# --- Service definitions ---
# Define services to run per worktree.
# Each service has its own command, directory, port range, and proxy port.

[services.frontend]
command = "pnpm run dev"
dir = "frontend"                        # relative to worktree root (empty = root)
port_range = { min = 3100, max = 3199 } # port allocation range for this service
proxy_port = 3000                        # proxy listens on this port
# Per-service env: injected into the process and written to the env file.
# Reference isola values with ${...}: accessories.<name>.url, services.<name>.url,
# services.<name>.port.
env = { NODE_ENV = "development", API_URL = "${services.backend.url}" }

[services.backend]
command = "go run ./cmd/server"
dir = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000
# env = { DATABASE_URL = "${accessories.database.url}" }   # (uncomment the accessory below)

# --- Per-worktree accessories (optional) ---
# Isolate stateful dependencies per worktree. isola brings up each accessory
# on 'up' and tears it down on 'down --prune'. It connects to your existing
# server and never manages the server itself. A service opts in by referencing
# ${accessories.<name>.url} in its env (there is no auto-injected key).
#
# [accessories.database]
# kind       = "postgres"                                       # driver
# server_url = "postgres://postgres@localhost:5432/postgres"    # existing server + maintenance db
# clone_from = "myapp_dev"                                      # seeded template copied per worktree
# name       = "myapp_${ISOLA_BRANCH_SLUG}"                     # per-worktree database name
# # url      = "postgres://app:app@localhost:5432/${db}"        # optional URL override (${db} = name)
#
# [accessories.cache]
# kind       = "redis"                                          # per-worktree Redis logical DB
# server_url = "redis://localhost:6379"

# --- Reverse proxy ---
# isola auto-starts the proxy on 'up' (http://<branch-slug>.<project>.localhost:<proxy_port>).
# [proxy]
# enabled    = true    # set false to start it yourself with 'isola proxy start'
# https      = false   # serve HTTPS with auto-generated certs
# auto_trust = true    # with https, trust the CA on first interactive 'up' (set false for manual 'isola trust')

# --- Per-worktree overrides (optional) ---
# [worktrees.main]
# services.frontend.port = 3100       # fixed port
#
# [worktrees."feature/auth"]
# services.backend.command = "source .venv/bin/activate && python manage.py runserver --settings=myapp.settings_auth 0.0.0.0:$PORT"
# services.backend.env = { DEBUG = "1" }
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
