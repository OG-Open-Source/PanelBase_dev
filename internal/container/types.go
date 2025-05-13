package container

import "net/http"

// ContainerStatus represents the possible states of a container.
type ContainerStatus string

const (
	StatusCreating ContainerStatus = "creating" // Initial state during creation
	StatusRunning  ContainerStatus = "running"  // Web server is active
	StatusStopped  ContainerStatus = "stopped"  // Intentionally stopped
	StatusError    ContainerStatus = "error"    // Encountered an error
)

// ContainerInfo holds runtime information and state about a container.
// This is primarily kept in memory.
type ContainerInfo struct {
	ID        string          `json:"id"`                  // Matches directory name and metadata ID
	Status    ContainerStatus `json:"status"`              // Current runtime status
	Port      int             `json:"port"`                // Port the web server listens on
	WebDir    string          `json:"webDir"`              // Path to the container's web root
	webServer *http.Server    `json:"-"`                   // Instance of the running web server
	LastError string          `json:"lastError,omitempty"` // Last error message at runtime
}

// ContainerMetadata represents the persistent configuration stored in container.yaml.
type ContainerMetadata struct {
	ID     string          `yaml:"id"`             // Mandatory: Should match the directory name
	Name   string          `yaml:"name,omitempty"` // Optional: User-friendly name
	Port   int             `yaml:"port"`           // Mandatory: Port for the web server
	Status ContainerStatus `yaml:"status"`         // Mandatory: Desired/last known status (running/stopped)
	// Add other persistent config fields here, e.g.:
	// AppliedTheme   string            `yaml:"applied_theme,omitempty"`
	// EnabledPlugins []string          `yaml:"enabled_plugins,omitempty"`
	// CustomEnv      map[string]string `yaml:"custom_env,omitempty"`
}

// IsValid checks mandatory fields for metadata.
func (m *ContainerMetadata) IsValid() bool {
	return m.ID != "" &&
		m.Port > 0 && // Port must be positive
		(m.Status == StatusRunning || m.Status == StatusStopped) // Initial status must be running or stopped
}
