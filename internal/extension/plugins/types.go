package plugins

import (
	"fmt"
	"net/url"

	// "os" // Removed as filepath.Separator is used
	"path/filepath"
	"strings"
)

// PluginMetadata represents the structure of the plugin.yaml file.
type PluginMetadata struct {
	Name        string   `yaml:"name"`        // Mandatory: Plugin display name
	Authors     []string `yaml:"authors"`     // Mandatory: List of authors
	Version     string   `yaml:"version"`     // Mandatory: Plugin version (e.g., semver)
	Description string   `yaml:"description"` // Mandatory: Short description
	SourceLink  string   `yaml:"source_link"` // Mandatory: Link to the source definition (e.g., raw JSON/YAML link)
	APIVersion  string   `yaml:"api_version"` // Mandatory: PanelBase API version compatibility (e.g., "v1")
	// Directory field removed
	Structure    map[string]interface{}    `yaml:"structure"`    // File structure and download links (can be nested)
	Dependencies map[string]string         `yaml:"dependencies"` // Optional: Go module dependencies (module path: version)
	Endpoints    map[string]EndpointConfig `yaml:"endpoints"`    // Optional: API endpoints provided by the plugin
}

// EndpointConfig defines the configuration for a single API endpoint.
type EndpointConfig struct {
	Methods     []string               `yaml:"methods"`               // Mandatory: List of supported HTTP methods (GET, POST, etc.)
	Description string                 `yaml:"description,omitempty"` // Optional: Description of the endpoint
	Input       map[string]interface{} `yaml:"input,omitempty"`       // JSON Schema for request body/parameters
	Output      map[string]interface{} `yaml:"output,omitempty"`      // JSON Schema for response body
}

// Validate performs a comprehensive validation of the plugin metadata.
func (m *PluginMetadata) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("plugin name is required and cannot be empty")
	}
	if len(m.Authors) == 0 {
		return fmt.Errorf("plugin authors are required")
	}
	for i, author := range m.Authors {
		if strings.TrimSpace(author) == "" {
			return fmt.Errorf("plugin author at index %d cannot be empty", i)
		}
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("plugin version is required and cannot be empty")
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("plugin description is required and cannot be empty")
	}

	if strings.TrimSpace(m.SourceLink) == "" {
		return fmt.Errorf("plugin source_link is required and cannot be empty")
	}
	if _, err := url.ParseRequestURI(m.SourceLink); err != nil {
		return fmt.Errorf("plugin source_link '%s' is not a valid URL: %w", m.SourceLink, err)
	}

	if strings.TrimSpace(m.APIVersion) == "" {
		return fmt.Errorf("plugin api_version is required and cannot be empty")
	}
	// TODO: Add more specific APIVersion format validation if needed (e.g., "v1", "v1.2")

	if m.Structure == nil || len(m.Structure) == 0 {
		return fmt.Errorf("plugin structure is required and cannot be empty")
	}
	if err := validatePluginStructureContent(m.Structure); err != nil {
		return fmt.Errorf("invalid plugin structure: %w", err)
	}

	// Validate Endpoints if present
	for path, endpointCfg := range m.Endpoints {
		if strings.TrimSpace(path) == "" || !strings.HasPrefix(path, "/") {
			return fmt.Errorf("invalid endpoint path '%s': must not be empty and must start with '/'", path)
		}
		if len(endpointCfg.Methods) == 0 {
			return fmt.Errorf("endpoint '%s' must have at least one HTTP method defined", path)
		}
		for _, method := range endpointCfg.Methods {
			if strings.TrimSpace(method) == "" { // Basic check, could be more specific (GET, POST, etc.)
				return fmt.Errorf("endpoint '%s' has an empty HTTP method", path)
			}
		}
		// TODO: Add validation for Input/Output JSON Schema if desired (e.g., basic structural check)
	}

	// Dependencies are optional, but if present, keys and values should not be empty.
	for modPath, modVersion := range m.Dependencies {
		if strings.TrimSpace(modPath) == "" {
			return fmt.Errorf("dependency module path cannot be empty")
		}
		if strings.TrimSpace(modVersion) == "" {
			return fmt.Errorf("dependency version for module '%s' cannot be empty", modPath)
		}
	}

	return nil
}

// IsValid is a basic check.
// Deprecated: Use Validate() for comprehensive validation.
func (m *PluginMetadata) IsValid() bool {
	return m.Name != "" &&
		len(m.Authors) > 0 &&
		m.Version != "" &&
		m.Description != "" &&
		m.SourceLink != "" &&
		m.APIVersion != "" &&
		// Directory check removed
		m.Structure != nil && len(m.Structure) > 0
}

// validatePluginStructureContent recursively validates the names and URLs within the Structure map.
// Similar to themes.validateStructureContent
func validatePluginStructureContent(structure map[string]interface{}) error {
	for key, value := range structure {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("file/directory name in structure cannot be empty")
		}
		if strings.ContainsAny(key, string(filepath.Separator)+"/\\") || key == "." || key == ".." {
			return fmt.Errorf("invalid file/directory name in structure: '%s'. It cannot contain path separators or be '.' or '..'", key)
		}

		switch v := value.(type) {
		case string: // URL for a file
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("URL for file '%s' in structure cannot be empty", key)
			}
			_, err := url.ParseRequestURI(v)
			if err != nil {
				return fmt.Errorf("invalid URL format for file '%s': '%s' (%w)", key, v, err)
			}
		case map[string]interface{}: // Sub-structure (directory)
			if err := validatePluginStructureContent(v); err != nil {
				return fmt.Errorf("invalid content in sub-directory '%s': %w", key, err)
			}
		default:
			return fmt.Errorf("invalid type for '%s' in structure: expected string (URL) or map (sub-directory)", key)
		}
	}
	return nil
}
