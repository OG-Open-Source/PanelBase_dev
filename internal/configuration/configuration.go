package configuration

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigDir         = "configs"
	defaultConfigPath        = "configs/config.yaml"
	defaultThemesStatePath   = "configs/themes.json"   // Default path for themes state
	defaultPluginsStatePath  = "configs/plugins.json"  // Default path for plugins state
	defaultCommandsStatePath = "configs/commands.json" // Default path for commands state
	defaultHost              = "0.0.0.0"
	minPort                  = 1024
	maxPort                  = 49151
)

// Config holds the application's configuration.
type Config struct {
	Version  string         `yaml:"version"`
	Server   ServerConfig   `yaml:"server"`
	Security SecurityConfig `yaml:"security"`
	// Paths    PathsConfig    `yaml:"paths"` // Removed Paths configuration
}

// PathsConfig holds configuration for various directory paths. - REMOVED
// type PathsConfig struct {
// 	ThemesDir     string `yaml:"themesDir"`
// 	PluginsDir    string `yaml:"pluginsDir"`
// 	CommandsDir   string `yaml:"commandsDir"`
// 	ContainersDir string `yaml:"containersDir"`
// }

// ServerConfig holds configuration related to the main PanelBase process and default container settings.
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// SecurityConfig holds security-related configuration.
type SecurityConfig struct {
	Secrets SecretsConfig `yaml:"secrets"`
}

// SecretsConfig holds configuration for generating secrets/IDs.
type SecretsConfig struct {
	Alphabet string `yaml:"alphabet"`
	Length   int    `yaml:"length"`
}

// LoadConfig loads the configuration from the specified path or the default path.
func LoadConfig(configPath ...string) (*Config, error) {
	path := defaultConfigPath
	if len(configPath) > 0 && configPath[0] != "" {
		path = configPath[0]
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == defaultConfigPath {
			fmt.Printf("Warning: Config file '%s' not found. Creating default config file.\n", path)
			defaultCfg := applyDefaults(&Config{})
			errWrite := writeDefaultConfig(path, defaultCfg)
			if errWrite != nil {
				fmt.Printf("Error: Failed to write default config file '%s': %v. Using defaults in memory.\n", path, errWrite)
			} else {
				fmt.Printf("Info: Default config file created at '%s'.\n", path)
			}
			return defaultCfg, nil
		}
		return nil, fmt.Errorf("failed to read config file '%s': %w", path, err)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file '%s': %w", path, err)
	}
	return applyDefaults(&cfg), nil
}

// applyDefaults sets default values for missing or invalid configuration options.
func applyDefaults(cfg *Config) *Config {
	if cfg.Version == "" {
		cfg.Version = "v1"
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = defaultHost
		fmt.Println("Info: Server host not specified. Using default:", defaultHost)
	}
	if cfg.Server.Port < minPort || cfg.Server.Port > maxPort {
		rand.Seed(time.Now().UnixNano())
		cfg.Server.Port = rand.Intn(maxPort-minPort+1) + minPort
		fmt.Printf("Info: Server port (for RPC) not specified or invalid. Using random port: %d\n", cfg.Server.Port)
	}
	if cfg.Security.Secrets.Alphabet == "" {
		cfg.Security.Secrets.Alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		fmt.Println("Warning: Security secrets alphabet was missing. Using default.")
	}
	if cfg.Security.Secrets.Length <= 0 {
		cfg.Security.Secrets.Length = 12
		fmt.Println("Warning: Security secrets length was missing or invalid. Using default (12).")
	}
	// Defaults for Paths - REMOVED
	// if cfg.Paths.ThemesDir == "" {
	// 	cfg.Paths.ThemesDir = "ext/themes"
	// 	fmt.Println("Info: Paths.ThemesDir not specified. Using default:", cfg.Paths.ThemesDir)
	// }
	// if cfg.Paths.PluginsDir == "" {
	// 	cfg.Paths.PluginsDir = "ext/plugins"
	// 	fmt.Println("Info: Paths.PluginsDir not specified. Using default:", cfg.Paths.PluginsDir)
	// }
	// if cfg.Paths.CommandsDir == "" {
	// 	cfg.Paths.CommandsDir = "ext/commands"
	// 	fmt.Println("Info: Paths.CommandsDir not specified. Using default:", cfg.Paths.CommandsDir)
	// }
	// if cfg.Paths.ContainersDir == "" {
	// 	cfg.Paths.ContainersDir = "containers" // Or a path under ext/ if preferred
	// 	fmt.Println("Info: Paths.ContainersDir not specified. Using default:", cfg.Paths.ContainersDir)
	// }
	return cfg
}

// writeDefaultConfig marshals the default config to YAML and writes it to the specified path.
func writeDefaultConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory '%s': %w", dir, err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal default config to YAML: %w", err)
	}
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write default config file '%s': %w", path, err)
	}
	return nil
}

// --- Extension State Management (e.g., for themes.json) ---

// InstalledThemeEntry represents a single theme entry.
// The LocalDir (e.g., "thm_abc123") will be the key in the Themes map.
type InstalledThemeEntry struct {
	ThmID         string `json:"thm_id"`      // Local directory ID (e.g., "thm_abc123"), matches the key
	Name          string `json:"name"`        // Theme name from metadata
	Version       string `json:"version"`     // Theme version from metadata
	SourceLink    string `json:"source_link"` // Canonical source link from the theme's metadata
	InstalledAt   string `json:"installed_at,omitempty"`
	LastUpdatedAt string `json:"last_updated,omitempty"` // Changed key name
}

// InstalledPluginEntry represents a single installed plugin entry.
// The PlgID (e.g., "plg_xyz789") will be the key in the Plugins map.
type InstalledPluginEntry struct {
	PlgID      string `json:"plg_id"`      // Local directory ID (e.g., "plg_xyz789"), matches the key
	Name       string `json:"name"`        // Plugin name from metadata
	Version    string `json:"version"`     // Plugin version from metadata
	SourceLink string `json:"source_link"` // Canonical source link from the plugin's metadata
}

// InstalledCommandEntry represents a single installed command entry.
// The Filename (e.g., "my-command.sh") will be the key in the Commands map.
type InstalledCommandEntry struct {
	Filename   string `json:"filename"`    // Command script filename (e.g., "my-command.sh"), matches the key
	Name       string `json:"name"`        // Command name from metadata (`command` field)
	Version    string `json:"version"`     // Command version from metadata
	SourceLink string `json:"source_link"` // Canonical source link from the command's metadata
}

// ExtensionStateStore is the top-level structure for managing extension states.
// We will use separate files for each extension type (themes.json, plugins.json, commands.json).
// This struct might become less relevant if loading/saving handles types directly.
// Keeping it for now as a conceptual container.
type ExtensionStateStore struct {
	Themes   map[string]InstalledThemeEntry   `json:"themes"`             // Key is LocalDir (thm_id)
	Plugins  map[string]InstalledPluginEntry  `json:"plugins,omitempty"`  // Key is PlgID (plg_id)
	Commands map[string]InstalledCommandEntry `json:"commands,omitempty"` // Key is Filename
}

// --- Generic Extension State Loading/Saving ---

// LoadThemesState loads the theme state from the themes JSON file.
func LoadThemesState(statePath ...string) (map[string]InstalledThemeEntry, error) {
	path := defaultThemesStatePath
	if len(statePath) > 0 && statePath[0] != "" {
		path = statePath[0]
	}
	return loadState[InstalledThemeEntry](path)
}

// SaveThemesState saves the theme state to the themes JSON file.
func SaveThemesState(themes map[string]InstalledThemeEntry, statePath ...string) error {
	path := defaultThemesStatePath
	if len(statePath) > 0 && statePath[0] != "" {
		path = statePath[0]
	}
	// Wrap the map in the expected top-level structure for themes.json
	dataToSave := map[string]interface{}{"themes": themes}
	return saveState(dataToSave, path)
}

// LoadPluginsState loads the plugin state from the plugins JSON file.
func LoadPluginsState(statePath ...string) (map[string]InstalledPluginEntry, error) {
	path := defaultPluginsStatePath
	if len(statePath) > 0 && statePath[0] != "" {
		path = statePath[0]
	}
	return loadState[InstalledPluginEntry](path)
}

// SavePluginsState saves the plugin state to the plugins JSON file.
func SavePluginsState(plugins map[string]InstalledPluginEntry, statePath ...string) error {
	path := defaultPluginsStatePath
	if len(statePath) > 0 && statePath[0] != "" {
		path = statePath[0]
	}
	// Wrap the map in the expected top-level structure for plugins.json
	dataToSave := map[string]interface{}{"plugins": plugins}
	return saveState(dataToSave, path)
}

// LoadCommandsState loads the command state from the commands JSON file.
func LoadCommandsState(statePath ...string) (map[string]InstalledCommandEntry, error) {
	path := defaultCommandsStatePath
	if len(statePath) > 0 && statePath[0] != "" {
		path = statePath[0]
	}
	return loadState[InstalledCommandEntry](path)
}

// SaveCommandsState saves the command state to the commands JSON file.
func SaveCommandsState(commands map[string]InstalledCommandEntry, statePath ...string) error {
	path := defaultCommandsStatePath
	if len(statePath) > 0 && statePath[0] != "" {
		path = statePath[0]
	}
	// Wrap the map in the expected top-level structure for commands.json
	dataToSave := map[string]interface{}{"commands": commands}
	return saveState(dataToSave, path)
}

// loadState is a generic function to load a map[string]T from a JSON file.
// It expects the JSON to have a top-level key (e.g., "themes", "plugins") whose value is the map.
func loadState[T any](path string) (map[string]T, error) {
	stateMap := make(map[string]T)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return an empty map if the file doesn't exist
			return stateMap, nil
		}
		return nil, fmt.Errorf("failed to read state file '%s': %w", path, err)
	}

	if len(data) == 0 {
		// Return an empty map if the file is empty
		return stateMap, nil
	}

	// We need to unmarshal into a temporary structure to extract the nested map
	var tempStore map[string]map[string]T
	err = json.Unmarshal(data, &tempStore)
	if err != nil {
		// Attempt to unmarshal directly into the map if the top-level key is missing (e.g., old format or direct map save)
		errDirect := json.Unmarshal(data, &stateMap)
		if errDirect == nil {
			// If direct unmarshal works, return the result but maybe log a warning about format?
			// fmt.Printf("Warning: State file '%s' might be missing the top-level key. Loaded directly.\n", path)
			return stateMap, nil
		}
		// If both fail, return the original error from unmarshalling the expected structure
		return nil, fmt.Errorf("failed to unmarshal state file '%s': %w", path, err)
	}

	// Extract the actual map from the first key found in the temp store
	// This assumes there's only one top-level key (like "themes", "plugins")
	for _, v := range tempStore {
		stateMap = v
		break // Only take the first map found
	}

	// Ensure the map is not nil even if the file contained an empty object under the key
	if stateMap == nil {
		stateMap = make(map[string]T)
	}

	return stateMap, nil
}

// saveState is a generic function to save any data structure (usually a map wrapper) to a JSON file.
func saveState(dataToSave interface{}, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory '%s': %w", dir, err)
	}

	data, err := json.MarshalIndent(dataToSave, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state data: %w", err)
	}

	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write state file '%s': %w", path, err)
	}
	return nil
}
