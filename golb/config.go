package golb

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// EnvPrefix is the prefix for environment variables
	EnvPrefix = "GOLB_"
	// Default algorithm
	DefaultLBAlgorithm = "round-robin"
	// Default EWMA alpha
	DefaultEWMAAlpha = 0.15
)

// Config holds all configuration parameters for the load balancer
type Config struct {
	ProxyPort              string        `yaml:"proxyPort"`
	BackendServers         []string      `yaml:"backendServers"`
	BackendWeights         []int         `yaml:"backendWeights,omitempty"` // For WRR
	HealthCheckPath        string        `yaml:"healthCheckPath"`
	InfoPath               string        `yaml:"infoPath"`
	HealthCheckInterval    time.Duration `yaml:"healthCheckInterval"`
	BackendRequestTimeout  time.Duration `yaml:"backendRequestTimeout"`
	LoadBalancingAlgorithm string        `yaml:"loadBalancingAlgorithm"`
	EWMAAlpha              float64       `yaml:"ewmaAlpha"` // For Least Response Time

	// Internal field, not loaded from yaml/env
	ConfigFile string `yaml:"-"`
}

// DefaultConfig returns a configuration with default values
func DefaultConfig() *Config {
	return &Config{
		ProxyPort:              ":8080",
		BackendServers:         []string{"http://localhost:9091", "http://localhost:9092"}, // Adjusted example defaults
		BackendWeights:         []int{},
		HealthCheckPath:        "/health",
		InfoPath:               "/info",
		HealthCheckInterval:    10 * time.Second,
		BackendRequestTimeout:  2 * time.Second,
		LoadBalancingAlgorithm: DefaultLBAlgorithm,
		EWMAAlpha:              DefaultEWMAAlpha,
		ConfigFile:             "",
	}
}

// LoadConfig applies configuration layers: Defaults -> File -> Env -> Flags
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	// --- Define Flags ---
	// Use default values from the DefaultConfig struct
	flagProxyPort := flag.String("port", cfg.ProxyPort, "Port for the proxy server (e.g., :8080) (Env: "+EnvPrefix+"PORT)")
	flagBackendServers := flag.String("backends", strings.Join(cfg.BackendServers, ","), "Comma-separated list of backend server URLs (Env: "+EnvPrefix+"BACKENDS)")
	flagBackendWeights := flag.String("weights", "", "Comma-separated list of backend weights (optional, for WRR) (Env: "+EnvPrefix+"WEIGHTS)") // Weights as string flag
	flagHealthPath := flag.String("health-path", cfg.HealthCheckPath, "Path for backend health checks (Env: "+EnvPrefix+"HEALTH_PATH)")
	flagInfoPath := flag.String("info-path", cfg.InfoPath, "Path for backend info endpoint (Env: "+EnvPrefix+"INFO_PATH)")
	flagHealthInterval := flag.Duration("health-interval", cfg.HealthCheckInterval, "Interval for health checks (e.g., 10s, 1m) (Env: "+EnvPrefix+"HEALTH_INTERVAL)")
	flagBackendTimeout := flag.Duration("backend-timeout", cfg.BackendRequestTimeout, "Timeout for backend health/info requests (e.g., 2s) (Env: "+EnvPrefix+"BACKEND_TIMEOUT)")
	flagConfigFile := flag.String("config", cfg.ConfigFile, "Path to YAML configuration file")
	flagLBAlgo := flag.String("lb-algo", cfg.LoadBalancingAlgorithm, "Load balancing algorithm: round-robin, least-connections, least-response-time, weighted-round-robin (Env: "+EnvPrefix+"LB_ALGORITHM)")
	flagEWMAAlpha := flag.Float64("ewma-alpha", cfg.EWMAAlpha, "EWMA smoothing factor (0 < alpha <= 1) for least-response-time (Env: "+EnvPrefix+"EWMA_ALPHA)")

	// Parse flags early to potentially get the config file path
	flag.Parse()

	// --- Load from Config File ---
	// Use the value parsed from flags OR the default ""
	if *flagConfigFile != "" {
		log.Printf("Loading configuration from file: %s", *flagConfigFile)
		if err := loadConfigFromFile(*flagConfigFile, cfg); err != nil {
			log.Printf("Warning: Failed to load config file '%s': %v. Using other sources.", *flagConfigFile, err)
			// Decide if a missing/invalid config file is fatal - here we just warn
		}
	}

	// --- Load from Environment Variables ---
	loadConfigFromEnv(cfg)

	// --- Apply Command Line Flags (Highest Priority) ---
	// Use flag.Visit to only apply flags that were actually set
	applyFlags(cfg, flagProxyPort, flagBackendServers, flagBackendWeights, flagHealthPath, flagInfoPath, flagHealthInterval, flagBackendTimeout, flagConfigFile, flagLBAlgo, flagEWMAAlpha)

	// --- Final Validation ---
	if len(cfg.BackendServers) == 0 || (len(cfg.BackendServers) == 1 && cfg.BackendServers[0] == "") {
		return nil, errors.New("configuration error: no backend servers specified")
	}
	if cfg.LoadBalancingAlgorithm == "weighted-round-robin" && len(cfg.BackendWeights) != len(cfg.BackendServers) {
		log.Printf("Warning: Mismatch between number of backends (%d) and weights (%d). Weights ignored unless count matches.", len(cfg.BackendServers), len(cfg.BackendWeights))
		// Optionally treat as error: return nil, errors.New("configuration error: backend count and weight count mismatch for weighted-round-robin")
	}
	if cfg.EWMAAlpha <= 0 || cfg.EWMAAlpha > 1.0 {
		log.Printf("Warning: Invalid EWMA alpha value (%.2f), using default %.2f.", cfg.EWMAAlpha, DefaultEWMAAlpha)
		cfg.EWMAAlpha = DefaultEWMAAlpha
	}

	log.Printf("Final Configuration Loaded: %+v", cfg)
	return cfg, nil
}

// loadConfigFromFile reads and parses the YAML file into the Config struct
func loadConfigFromFile(filePath string, cfg *Config) error {
	yamlFile, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}
	// Unmarshal into the existing cfg pointer to overwrite defaults/previous values
	err = yaml.Unmarshal(yamlFile, cfg)
	if err != nil {
		return fmt.Errorf("could not parse YAML: %w", err)
	}
	return nil
}

// loadConfigFromEnv loads configuration from environment variables, overwriting existing values
func loadConfigFromEnv(cfg *Config) {
	if port := os.Getenv(EnvPrefix + "PORT"); port != "" {
		cfg.ProxyPort = port
	}
	if backends := os.Getenv(EnvPrefix + "BACKENDS"); backends != "" {
		cfg.BackendServers = parseCommaSeparatedString(backends)
	}
	if weightsStr := os.Getenv(EnvPrefix + "WEIGHTS"); weightsStr != "" {
		weights, err := parseCommaSeparatedInts(weightsStr)
		if err == nil {
			cfg.BackendWeights = weights
		} else {
			log.Printf("Warning: Invalid format for env var %sWEIGHTS: %v", EnvPrefix, err)
		}
	}
	if path := os.Getenv(EnvPrefix + "HEALTH_PATH"); path != "" {
		cfg.HealthCheckPath = path
	}
	if path := os.Getenv(EnvPrefix + "INFO_PATH"); path != "" {
		cfg.InfoPath = path
	}
	if intervalStr := os.Getenv(EnvPrefix + "HEALTH_INTERVAL"); intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil {
			cfg.HealthCheckInterval = d
		} else {
			log.Printf("Warning: Invalid format for env var %sHEALTH_INTERVAL: %v", EnvPrefix, err)
		}
	}
	if timeoutStr := os.Getenv(EnvPrefix + "BACKEND_TIMEOUT"); timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil {
			cfg.BackendRequestTimeout = d
		} else {
			log.Printf("Warning: Invalid format for env var %sBACKEND_TIMEOUT: %v", EnvPrefix, err)
		}
	}
	if algo := os.Getenv(EnvPrefix + "LB_ALGORITHM"); algo != "" {
		cfg.LoadBalancingAlgorithm = strings.ToLower(algo)
	}
	if alphaStr := os.Getenv(EnvPrefix + "EWMA_ALPHA"); alphaStr != "" {
		if alpha, err := strconv.ParseFloat(alphaStr, 64); err == nil {
			cfg.EWMAAlpha = alpha
		} else {
			log.Printf("Warning: Invalid format for env var %sEWMA_ALPHA: %v", EnvPrefix, err)
		}
	}
}

// applyFlags overwrites cfg fields if the corresponding flag was explicitly set on the command line
func applyFlags(cfg *Config, flagProxyPort *string, flagBackendServers *string, flagBackendWeights *string, flagHealthPath *string, flagInfoPath *string, flagHealthInterval *time.Duration, flagBackendTimeout *time.Duration, flagConfigFile *string, flagLBAlgo *string, flagEWMAAlpha *float64) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			cfg.ProxyPort = *flagProxyPort
		case "backends":
			cfg.BackendServers = parseCommaSeparatedString(*flagBackendServers)
		case "weights":
			weights, err := parseCommaSeparatedInts(*flagBackendWeights)
			if err == nil {
				cfg.BackendWeights = weights
			} else {
				log.Printf("Warning: Invalid format for flag -weights: %v", err)
			}
		case "health-path":
			cfg.HealthCheckPath = *flagHealthPath
		case "info-path":
			cfg.InfoPath = *flagInfoPath
		case "health-interval":
			cfg.HealthCheckInterval = *flagHealthInterval
		case "backend-timeout":
			cfg.BackendRequestTimeout = *flagBackendTimeout
		case "config":
			cfg.ConfigFile = *flagConfigFile // Store the used path
		case "lb-algo":
			cfg.LoadBalancingAlgorithm = strings.ToLower(*flagLBAlgo)
		case "ewma-alpha":
			cfg.EWMAAlpha = *flagEWMAAlpha
		}
	})
}

// --- Helper Functions ---

func parseCommaSeparatedString(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseCommaSeparatedInts(s string) ([]int, error) {
	if s == "" {
		return []int{}, nil
	}
	parts := strings.Split(s, ",")
	ints := make([]int, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue // Skip empty parts, maybe log warning?
		}
		val, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid integer '%s' in comma-separated list", trimmed)
		}
		ints = append(ints, val)
	}
	return ints, nil
}
