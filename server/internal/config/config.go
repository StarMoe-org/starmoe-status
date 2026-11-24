package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds runtime configuration loaded from environment variables and monitors.yaml.
type Config struct {
	ListenAddr     string
	DBPath         string
	MonitorsConfig string
	PublicBaseURL  string

	// ClusterTokenHashes maps cluster name (DE, JP1, JP2, US) -> 32-byte SHA-256 digest.
	ClusterTokenHashes map[string][32]byte
	BotTokenHash       *[32]byte

	// Admin auth
	CFAccessTeamDomain string
	CFAccessAUD        string
	AdminDevTokenHash  *[32]byte

	// Email alerting
	EmailPolicy  string // off | fallback | p0_always
	EmailFrom    string
	EmailTo      []string
	ResendAPIKey string
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPass     string

	// Optional external dead-man's-switch ping URL (e.g. Healthchecks.io)
	HealthcheckPingURL string

	// Timing knobs
	ProbeIntervalSec     int
	HeartbeatDegradedSec int
	HeartbeatDownSec     int

	// Parsed monitors.yaml
	Groups   []GroupConfig
	Monitors []MonitorConfig
}

// MonitorsFile is the root YAML schema for config/monitors.yaml.
type MonitorsFile struct {
	Groups   []GroupConfig   `yaml:"groups"`
	Monitors []MonitorConfig `yaml:"monitors"`
}

// GroupConfig defines a public product group.
type GroupConfig struct {
	Key  string `yaml:"key"`
	Name string `yaml:"name"`
}

// ExtraURLConfig defines an additional HTTP endpoint checked alongside a monitor's primary URL.
type ExtraURLConfig struct {
	URL            string `yaml:"url"`
	Method         string `yaml:"method"`
	ExpectedStatus []int  `yaml:"expected_status"`
}

// MonitorConfig defines one monitored component.
type MonitorConfig struct {
	ID             string           `yaml:"id"`
	Name           string           `yaml:"name"`
	GroupKey       string           `yaml:"group_key"`
	Kind           string           `yaml:"kind"` // http | tls | tcp | agent_node | agent_deep
	Cluster        string           `yaml:"cluster"`
	Visibility     string           `yaml:"visibility"` // public | internal
	Severity       string           `yaml:"severity"`   // P0 | P1 | P2
	URL            string           `yaml:"url"`
	ExtraURLs      []ExtraURLConfig `yaml:"extra_urls"`
	Method         string           `yaml:"method"`
	ExpectedStatus []int            `yaml:"expected_status"`
	JSONAssert     string           `yaml:"json_assert"`
	TCPAddr        string           `yaml:"tcp_addr"`
	TimeoutSec     int              `yaml:"timeout_sec"`
	IntervalSec    int              `yaml:"interval_sec"`
	FailThreshold  int              `yaml:"fail_threshold"`
	OKThreshold    int              `yaml:"ok_threshold"`
	SortOrder      int              `yaml:"sort_order"`
}

// Load reads environment variables and parses the monitors YAML file.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:           envOr("LISTEN_ADDR", ":8080"),
		DBPath:               envOr("DB_PATH", "./status.db"),
		MonitorsConfig:       envOr("MONITORS_CONFIG", "config/monitors.yaml"),
		PublicBaseURL:        strings.TrimRight(envOr("PUBLIC_BASE_URL", "https://status.star.moe"), "/"),
		ClusterTokenHashes:   make(map[string][32]byte),
		CFAccessTeamDomain:   strings.TrimSpace(os.Getenv("CF_ACCESS_TEAM_DOMAIN")),
		CFAccessAUD:          strings.TrimSpace(os.Getenv("CF_ACCESS_AUD")),
		EmailPolicy:          strings.ToLower(envOr("EMAIL_POLICY", "off")),
		EmailFrom:            strings.TrimSpace(os.Getenv("EMAIL_FROM")),
		EmailTo:              splitCSV(os.Getenv("EMAIL_TO")),
		ResendAPIKey:         strings.TrimSpace(os.Getenv("RESEND_API_KEY")),
		SMTPHost:             strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:             envInt("SMTP_PORT", 587),
		SMTPUser:             strings.TrimSpace(os.Getenv("SMTP_USER")),
		SMTPPass:             os.Getenv("SMTP_PASS"),
		HealthcheckPingURL:   strings.TrimSpace(os.Getenv("HEALTHCHECK_PING_URL")),
		ProbeIntervalSec:     envInt("PROBE_INTERVAL_SEC", 60),
		HeartbeatDegradedSec: envInt("HEARTBEAT_DEGRADED_SEC", 120),
		HeartbeatDownSec:     envInt("HEARTBEAT_DOWN_SEC", 180),
	}

	switch cfg.EmailPolicy {
	case "off", "fallback", "p0_always":
	default:
		return nil, fmt.Errorf("invalid EMAIL_POLICY %q (expected off, fallback, or p0_always)", cfg.EmailPolicy)
	}

	for _, cluster := range []string{"DE", "JP1", "JP2", "US"} {
		if raw := strings.TrimSpace(os.Getenv("CLUSTER_TOKEN_" + cluster)); raw != "" {
			h, err := ParseTokenHash(raw)
			if err != nil {
				return nil, fmt.Errorf("CLUSTER_TOKEN_%s: %w", cluster, err)
			}
			cfg.ClusterTokenHashes[cluster] = h
		}
	}

	if raw := strings.TrimSpace(os.Getenv("BOT_TOKEN")); raw != "" {
		h, err := ParseTokenHash(raw)
		if err != nil {
			return nil, fmt.Errorf("BOT_TOKEN: %w", err)
		}
		cfg.BotTokenHash = &h
	}

	if raw := strings.TrimSpace(os.Getenv("ADMIN_DEV_TOKEN")); raw != "" {
		h, err := ParseTokenHash(raw)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_DEV_TOKEN: %w", err)
		}
		cfg.AdminDevTokenHash = &h
	}

	mf, err := LoadMonitorsFile(cfg.MonitorsConfig)
	if err != nil {
		return nil, err
	}
	cfg.Groups = mf.Groups
	cfg.Monitors = mf.Monitors
	return cfg, nil
}

// LoadMonitorsFile parses and validates a monitors YAML file.
func LoadMonitorsFile(path string) (*MonitorsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read monitors config %q: %w", path, err)
	}
	var mf MonitorsFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parse monitors config %q: %w", path, err)
	}
	seen := make(map[string]struct{}, len(mf.Monitors))
	for i := range mf.Monitors {
		m := &mf.Monitors[i]
		if m.ID == "" {
			return nil, fmt.Errorf("monitor at index %d missing id", i)
		}
		if _, dup := seen[m.ID]; dup {
			return nil, fmt.Errorf("duplicate monitor id %q", m.ID)
		}
		seen[m.ID] = struct{}{}
		if m.Name == "" {
			m.Name = m.ID
		}
		if m.Visibility == "" {
			m.Visibility = "public"
		}
		if m.Severity == "" {
			m.Severity = "P1"
		}
		if m.Method == "" {
			m.Method = "GET"
		}
		if len(m.ExpectedStatus) == 0 && (m.Kind == "http" || m.Kind == "tls") {
			m.ExpectedStatus = []int{200}
		}
		if m.TimeoutSec <= 0 {
			m.TimeoutSec = 10
		}
		if m.IntervalSec <= 0 {
			m.IntervalSec = 60
		}
		if m.FailThreshold <= 0 {
			m.FailThreshold = 3
		}
		if m.OKThreshold <= 0 {
			m.OKThreshold = 2
		}
		if m.SortOrder == 0 {
			m.SortOrder = i + 1
		}
		for j := range m.ExtraURLs {
			if m.ExtraURLs[j].Method == "" {
				m.ExtraURLs[j].Method = "GET"
			}
			if len(m.ExtraURLs[j].ExpectedStatus) == 0 {
				m.ExtraURLs[j].ExpectedStatus = []int{200}
			}
		}
	}
	return &mf, nil
}

// ParseTokenHash accepts either a raw secret string (returning SHA-256 of it)
// or a pre-hashed "sha256:<64-hex-chars>" value.
func ParseTokenHash(val string) ([32]byte, error) {
	var out [32]byte
	if strings.HasPrefix(val, "sha256:") {
		hexStr := strings.TrimPrefix(val, "sha256:")
		b, err := hex.DecodeString(hexStr)
		if err != nil || len(b) != 32 {
			return out, fmt.Errorf("invalid sha256 token hash")
		}
		copy(out[:], b)
		return out, nil
	}
	return sha256.Sum256([]byte(val)), nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
