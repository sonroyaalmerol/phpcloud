package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Config holds the complete runtime configuration
type Config struct {
	AppProfile string `yaml:"app_profile" mapstructure:"app_profile"`

	Server      ServerConfig      `yaml:"server" mapstructure:"server"`
	PHPFPM      PHPFPMConfig      `yaml:"php_fpm" mapstructure:"php_fpm"`
	Session     SessionConfig     `yaml:"session" mapstructure:"session"`
	Cluster     ClusterConfig     `yaml:"cluster" mapstructure:"cluster"`
	DB          DBConfig          `yaml:"db" mapstructure:"db"`
	Migration   MigrationConfig   `yaml:"migration" mapstructure:"migration"`
	Cron        CronConfig        `yaml:"cron" mapstructure:"cron"`
	StaticFiles StaticFilesConfig `yaml:"static_files" mapstructure:"static_files"`
	Maintenance MaintenanceConfig `yaml:"maintenance" mapstructure:"maintenance"`
	Logging     LoggingConfig     `yaml:"logging" mapstructure:"logging"`
	Metrics     MetricsConfig     `yaml:"metrics" mapstructure:"metrics"`
	SQLProxy    SQLProxyConfig    `yaml:"sql_proxy" mapstructure:"sql_proxy"`

	// Loaded profile (merged with base config)
	Profile *AppProfile `yaml:"-"`
}

// ServerConfig defines HTTP server settings
type ServerConfig struct {
	HTTPPort       int           `yaml:"http_port" mapstructure:"http_port"`
	GossipPort     int           `yaml:"gossip_port" mapstructure:"gossip_port"`
	MetricsPort    int           `yaml:"metrics_port" mapstructure:"metrics_port"`
	ReadTimeout    time.Duration `yaml:"read_timeout" mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `yaml:"write_timeout" mapstructure:"write_timeout"`
	MaxRequestBody int64         `yaml:"max_request_body" mapstructure:"max_request_body"`
}

// PHPFPMConfig defines PHP-FPM settings
type PHPFPMConfig struct {
	Socket          string            `yaml:"socket" mapstructure:"socket"`
	Binary          string            `yaml:"binary" mapstructure:"binary"`
	Config          string            `yaml:"config" mapstructure:"config"`
	PoolSizeMin     int               `yaml:"pool_size_min" mapstructure:"pool_size_min"`
	PoolSizeMax     int               `yaml:"pool_size_max" mapstructure:"pool_size_max"`
	PHPINIOverrides map[string]string `yaml:"php_ini_overrides" mapstructure:"php_ini_overrides"`
	External        bool              `yaml:"external" mapstructure:"external"` // If true, connect to existing FPM instead of spawning
}

// SQLProxyConfig defines SQL proxy settings for database migrations
type SQLProxyConfig struct {
	Enabled    bool   `yaml:"enabled" mapstructure:"enabled"`
	ListenAddr string `yaml:"listen_addr" mapstructure:"listen_addr"`
	TargetHost string `yaml:"target_host" mapstructure:"target_host"`
	TargetPort int    `yaml:"target_port" mapstructure:"target_port"`
}

// SessionConfig defines session management settings
type SessionConfig struct {
	Enabled     bool          `yaml:"enabled" mapstructure:"enabled"`
	Backend     string        `yaml:"backend" mapstructure:"backend"`
	CookieName  string        `yaml:"cookie_name" mapstructure:"cookie_name"`
	TTL         time.Duration `yaml:"ttl" mapstructure:"ttl"`
	LockTimeout time.Duration `yaml:"lock_timeout" mapstructure:"lock_timeout"`
}

// ClusterConfig defines cluster coordination settings
type ClusterConfig struct {
	Enabled     bool     `yaml:"enabled" mapstructure:"enabled"`
	Discovery   string   `yaml:"discovery" mapstructure:"discovery"`
	DNSService  string   `yaml:"dns_service" mapstructure:"dns_service"`
	StaticPeers []string `yaml:"static_peers" mapstructure:"static_peers"`
	NodeName    string   `yaml:"node_name" mapstructure:"node_name"`
}

// DBConfig defines embedded database settings
type DBConfig struct {
	Path string `yaml:"path" mapstructure:"path"` // Path to embedded database directory
}

// MigrationConfig defines migration settings
type MigrationConfig struct {
	Enabled      bool          `yaml:"enabled" mapstructure:"enabled"`
	Command      []string      `yaml:"command" mapstructure:"command"`
	LockKey      string        `yaml:"lock_key" mapstructure:"lock_key"`
	LockTimeout  time.Duration `yaml:"lock_timeout" mapstructure:"lock_timeout"`
	VersionQuery string        `yaml:"version_query" mapstructure:"version_query"`
	VersionFile  string        `yaml:"version_file" mapstructure:"version_file"`
	QueueSize    int           `yaml:"queue_size" mapstructure:"queue_size"` // Max queries to queue during migration
}

// CronConfig defines cron job settings
type CronConfig struct {
	Enabled    bool      `yaml:"enabled" mapstructure:"enabled"`
	LeaderOnly bool      `yaml:"leader_only" mapstructure:"leader_only"`
	Jobs       []CronJob `yaml:"jobs" mapstructure:"jobs"`
}

// CronJob defines a single cron job
type CronJob struct {
	Name     string   `yaml:"name" mapstructure:"name"`
	Schedule string   `yaml:"schedule" mapstructure:"schedule"`
	Type     string   `yaml:"type" mapstructure:"type"`
	Path     string   `yaml:"path" mapstructure:"path"`
	Command  []string `yaml:"command" mapstructure:"command"`
}

// StaticFilesConfig defines static file serving settings
type StaticFilesConfig struct {
	Enabled    bool     `yaml:"enabled" mapstructure:"enabled"`
	Extensions []string `yaml:"extensions" mapstructure:"extensions"`
	Root       string   `yaml:"root" mapstructure:"root"`
}

// MaintenanceConfig defines maintenance mode settings
type MaintenanceConfig struct {
	Enabled      bool          `yaml:"enabled" mapstructure:"enabled"`
	ResponseCode int           `yaml:"response_code" mapstructure:"response_code"`
	RetryAfter   time.Duration `yaml:"retry_after" mapstructure:"retry_after"`
	Message      string        `yaml:"message" mapstructure:"message"`
}

// LoggingConfig defines logging settings
type LoggingConfig struct {
	Level       string `yaml:"level" mapstructure:"level"`
	Format      string `yaml:"format" mapstructure:"format"`
	RequestLogs bool   `yaml:"request_logs" mapstructure:"request_logs"`
}

// MetricsConfig defines metrics settings
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled" mapstructure:"enabled"`
	Path    string `yaml:"path" mapstructure:"path"`
}

// AppProfile defines application-specific settings
type AppProfile struct {
	Name            string                 `yaml:"name"`
	VersionDetect   VersionDetectConfig    `yaml:"version_detect"`
	Session         ProfileSessionConfig   `yaml:"session"`
	Migration       ProfileMigrationConfig `yaml:"migration"`
	Cron            ProfileCronConfig      `yaml:"cron"`
	WritablePaths   []string               `yaml:"writable_paths"`
	StaticFiles     ProfileStaticConfig    `yaml:"static_files"`
	PHPINIOverrides map[string]string      `yaml:"php_ini_overrides"`
}

// VersionDetectConfig defines how to detect application version
type VersionDetectConfig struct {
	File    string `yaml:"file"`
	Regex   string `yaml:"regex"`
	DBQuery string `yaml:"db_query"`
}

// ProfileSessionConfig defines profile-specific session settings
type ProfileSessionConfig struct {
	CookieName string            `yaml:"cookie_name"`
	PHPINI     map[string]string `yaml:"php_ini"`
}

// ProfileMigrationConfig defines profile-specific migration settings
type ProfileMigrationConfig struct {
	Command   [][]string `yaml:"command"`
	PreHooks  [][]string `yaml:"pre_hooks"`
	PostHooks [][]string `yaml:"post_hooks"`
}

// ProfileCronConfig defines profile-specific cron settings
type ProfileCronConfig struct {
	Jobs []CronJob `yaml:"jobs"`
}

// ProfileStaticConfig defines profile-specific static file settings
type ProfileStaticConfig struct {
	AdditionalExtensions []string `yaml:"additional_extensions"`
}

// Load reads configuration from file and environment variables
func Load(path string, logger *zap.Logger) (*Config, error) {
	cfg := &Config{}

	// Set defaults
	setDefaults(cfg)

	// Load from file if it exists
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
		logger.Info("Loaded configuration from file", zap.String("path", path))
	} else {
		logger.Info("Config file not found, using defaults", zap.String("path", path))
	}

	// Override with environment variables
	if err := loadFromEnv(cfg); err != nil {
		return nil, fmt.Errorf("failed to load from environment: %w", err)
	}

	// Load app profile if specified
	if cfg.AppProfile != "" {
		profile, err := LoadProfile(cfg.AppProfile, logger)
		if err != nil {
			logger.Warn("Failed to load app profile", zap.String("profile", cfg.AppProfile), zap.Error(err))
		} else {
			cfg.Profile = profile
			mergeProfile(cfg, profile)
			logger.Info("Loaded app profile", zap.String("profile", cfg.AppProfile))
		}
	}

	// Validate configuration
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// setDefaults sets default configuration values
func setDefaults(cfg *Config) {
	cfg.Server.HTTPPort = 8080
	cfg.Server.GossipPort = 7946
	cfg.Server.MetricsPort = 9090
	cfg.Server.ReadTimeout = 30 * time.Second
	cfg.Server.WriteTimeout = 120 * time.Second
	cfg.Server.MaxRequestBody = 10 * 1024 * 1024 * 1024 // 10GB

	cfg.PHPFPM.Socket = "unix:///run/php-fpm.sock"
	cfg.PHPFPM.Binary = "php-fpm"
	cfg.PHPFPM.Config = "/etc/php/php-fpm.conf"
	cfg.PHPFPM.PoolSizeMin = 2
	cfg.PHPFPM.PoolSizeMax = 20

	cfg.Session.Enabled = true
	cfg.Session.Backend = "db"
	cfg.Session.CookieName = "PHPSESSID"
	cfg.Session.TTL = 24 * time.Hour
	cfg.Session.LockTimeout = 30 * time.Second

	cfg.Cluster.Enabled = true
	cfg.Cluster.Discovery = "dns"
	cfg.Cluster.DNSService = "phpcloud-gossip.default.svc.cluster.local"

	cfg.DB.Path = "/var/lib/phpcloud/db"

	cfg.Migration.Enabled = true
	cfg.Migration.LockKey = "phpcloud:migration"
	cfg.Migration.LockTimeout = 30 * time.Minute
	cfg.Migration.QueueSize = 10000

	cfg.Cron.Enabled = true
	cfg.Cron.LeaderOnly = true

	cfg.StaticFiles.Enabled = true
	cfg.StaticFiles.Extensions = []string{
		".css", ".js", ".png", ".jpg", ".jpeg", ".gif",
		".svg", ".ico", ".woff", ".woff2", ".ttf", ".map",
	}
	cfg.StaticFiles.Root = "/var/www/html"

	cfg.Maintenance.Enabled = true
	cfg.Maintenance.ResponseCode = 503
	cfg.Maintenance.RetryAfter = 30 * time.Second
	cfg.Maintenance.Message = "Service is updating, please wait."

	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Logging.RequestLogs = true

	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = "/metrics"
}

// loadFromEnv loads configuration from environment variables
func loadFromEnv(cfg *Config) error {
	v := viper.New()
	v.SetEnvPrefix("PHPCLOUD")
	v.AutomaticEnv()

	// Map environment variables to config fields
	// CONFIG is already handled in Load()
	if v.IsSet("DB_PATH") {
		cfg.DB.Path = v.GetString("DB_PATH")
	}
	if v.IsSet("LOG_LEVEL") {
		cfg.Logging.Level = v.GetString("LOG_LEVEL")
	}
	if v.IsSet("NODE_NAME") {
		cfg.Cluster.NodeName = v.GetString("NODE_NAME")
	}
	if v.IsSet("PROFILE") {
		cfg.AppProfile = v.GetString("PROFILE")
	}

	return nil
}

// LoadProfile loads an application profile from the profiles directory
func LoadProfile(name string, logger *zap.Logger) (*AppProfile, error) {
	paths := []string{
		fmt.Sprintf("profiles/%s.yaml", name),
		fmt.Sprintf("/phpcloud/profiles/%s.yaml", name),
		fmt.Sprintf("/etc/phpcloud/profiles/%s.yaml", name),
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			profile := &AppProfile{}
			if err := yaml.Unmarshal(data, profile); err != nil {
				return nil, fmt.Errorf("failed to parse profile %s: %w", path, err)
			}
			return profile, nil
		}
	}

	return nil, fmt.Errorf("profile not found: %s", name)
}

// mergeProfile merges profile settings into the main config
func mergeProfile(cfg *Config, profile *AppProfile) {
	// Merge session cookie name
	if profile.Session.CookieName != "" {
		cfg.Session.CookieName = profile.Session.CookieName
	}

	// Merge PHP INI overrides
	if cfg.PHPFPM.PHPINIOverrides == nil {
		cfg.PHPFPM.PHPINIOverrides = make(map[string]string)
	}
	for k, v := range profile.PHPINIOverrides {
		cfg.PHPFPM.PHPINIOverrides[k] = v
	}
	for k, v := range profile.Session.PHPINI {
		cfg.PHPFPM.PHPINIOverrides[k] = v
	}

	// Merge static file extensions
	if len(profile.StaticFiles.AdditionalExtensions) > 0 {
		extMap := make(map[string]bool)
		for _, ext := range cfg.StaticFiles.Extensions {
			extMap[ext] = true
		}
		for _, ext := range profile.StaticFiles.AdditionalExtensions {
			if !extMap[ext] {
				cfg.StaticFiles.Extensions = append(cfg.StaticFiles.Extensions, ext)
			}
		}
	}

	// Merge migration command if not set in config
	if len(cfg.Migration.Command) == 0 && len(profile.Migration.Command) > 0 {
		cfg.Migration.Command = profile.Migration.Command[0]
	}

	// Merge cron jobs if not set in config
	if len(cfg.Cron.Jobs) == 0 && len(profile.Cron.Jobs) > 0 {
		cfg.Cron.Jobs = profile.Cron.Jobs
	}
}

// validate validates the configuration
func validate(cfg *Config) error {
	if cfg.Session.Backend != "db" && cfg.Session.Backend != "memory" {
		return fmt.Errorf("unsupported session backend: %s (use db or memory)", cfg.Session.Backend)
	}

	if cfg.DB.Path == "" {
		return fmt.Errorf("database path is required")
	}

	return nil
}
