package themes

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	// "path/filepath" // Removed as Directory validation is removed
	"strings"
)

// AuthorDetail represents the details of a theme author.
type AuthorDetail struct {
	Name  string `yaml:"name" json:"name"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
}

// AssetDetail represents an asset within the theme structure, including its URL and checksum.
type AssetDetail struct {
	URL string `yaml:"url" json:"url"`
	Sum string `yaml:"sum" json:"sum"` // SHA256 hash
}

// ThemeMetadata represents the structure of the theme.yaml file.
type ThemeMetadata struct {
	Name        string         `yaml:"name" json:"name"`
	Authors     []AuthorDetail `yaml:"authors" json:"authors"`
	Version     string         `yaml:"version" json:"version"`
	Description string   `yaml:"description" json:"description"`
	SourceLink  string   `yaml:"source_link" json:"source_link"`
	// Directory field removed
	Structure     map[string]interface{} `yaml:"structure" json:"structure"`
	InstalledAt   string                 `yaml:"installed_at,omitempty" json:"installed_at,omitempty"`
	LastUpdatedAt string                 `yaml:"last_updated_at,omitempty" json:"last_updated,omitempty"` // Changed JSON key name
}

// Validate performs a comprehensive validation of the theme metadata.
func (m *ThemeMetadata) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("theme name is required and cannot be empty")
	}
	if len(m.Authors) == 0 {
		return fmt.Errorf("theme authors are required")
	}
	for i, author := range m.Authors {
		if strings.TrimSpace(author.Name) == "" {
			return fmt.Errorf("theme author name at index %d cannot be empty", i)
		}
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("theme version is required and cannot be empty")
	}
	// TODO: Add more specific version format validation if needed (e.g., semver)

	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("theme description is required and cannot be empty")
	}

	if strings.TrimSpace(m.SourceLink) == "" {
		return fmt.Errorf("theme source_link is required and cannot be empty")
	}
	// Validate m.SourceLink itself as a URL.
	// The structure values are now expected to be absolute URLs, so sourceLinkBase is not passed down.
	parsedSourceLink, err := url.ParseRequestURI(m.SourceLink)
	if err != nil {
		return fmt.Errorf("theme source_link '%s' is not a valid URL: %w", m.SourceLink, err)
	}
	if parsedSourceLink.Scheme != "http" && parsedSourceLink.Scheme != "https" {
		return fmt.Errorf("theme source_link URL scheme must be http or https, got '%s' for '%s'", parsedSourceLink.Scheme, m.SourceLink)
	}

	// Directory validation removed

	if len(m.Structure) == 0 {
		return fmt.Errorf("theme structure is required and cannot be empty")
	}
	// validateStructureContent now expects absolute URLs in m.Structure and doesn't need sourceLinkBase.
	if err := validateStructureContent(m.Structure); err != nil {
		return fmt.Errorf("invalid theme structure: %w", err)
	}

	return nil
}

// IsValid is a basic check, kept for compatibility or simpler checks if needed,
// but Validate() is preferred for comprehensive validation.
// Deprecated: Use Validate() for comprehensive validation.
func (m *ThemeMetadata) IsValid() bool {
	// Basic non-empty checks
	return m.Name != "" &&
		len(m.Authors) > 0 &&
		m.Version != "" &&
		m.Description != "" &&
		m.SourceLink != "" &&
		// Directory check removed
		m.Structure != nil && len(m.Structure) > 0
}

// validateStructureContent recursively validates the names and URLs within the Structure map.
func validateStructureContent(structure map[string]interface{}) error { // Removed sourceLinkBase parameter
	for key, value := range structure {
		// Validate key (file/directory name)
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("file/directory name in structure cannot be empty")
		}
		if strings.ContainsAny(key, string(os.PathSeparator)+"/\\") || key == "." || key == ".." {
			return fmt.Errorf("invalid file/directory name in structure: '%s'. It cannot contain path separators ('/' or '\\') or be exactly '.' or '..'", key)
		}

		switch v := value.(type) {
		case AssetDetail: // Value is an AssetDetail struct (representing a file)
			asset := v // v is already of type AssetDetail
			if strings.TrimSpace(asset.URL) == "" {
				return fmt.Errorf("URL for asset '%s' in structure cannot be empty", key)
			}
			parsedURL, err := url.ParseRequestURI(asset.URL)
			if err != nil {
				return fmt.Errorf("invalid URL format for asset '%s': value '%s' is not a valid URL (%w)", key, asset.URL, err)
			}
			// Validate Scheme: must not be empty.
			// Validate Host: must not be empty UNLESS scheme is 'file'.
			if parsedURL.Scheme == "" {
				return fmt.Errorf("invalid URL for asset '%s': value '%s' must have a scheme", key, asset.URL)
			}
			if parsedURL.Scheme != "file" && parsedURL.Host == "" {
				return fmt.Errorf("invalid URL for asset '%s': value '%s' (scheme '%s') must have a host", key, asset.URL, parsedURL.Scheme)
			}

			if strings.TrimSpace(asset.Sum) == "" {
				return fmt.Errorf("sum for asset '%s' in structure cannot be empty", key)
			}
			matched, _ := regexp.MatchString("^[a-f0-9]{64}$", strings.ToLower(asset.Sum))
			if !matched {
				return fmt.Errorf("invalid sum format for asset '%s': must be a 64-character SHA256 hex string", key)
			}

		case map[string]interface{}: // Value is a map, could be an AssetDetail-like map or a sub-directory
			// Try to interpret as an AssetDetail-like map first
			urlVal, urlOk := v["url"]
			sumVal, sumOk := v["sum"]

			if urlOk && sumOk { // It has 'url' and 'sum' keys, potentially an AssetDetail
				urlString, urlIsString := urlVal.(string)
				sumString, sumIsString := sumVal.(string)

				if urlIsString && sumIsString { // Both 'url' and 'sum' are strings, treat as AssetDetail
					if strings.TrimSpace(urlString) == "" {
						return fmt.Errorf("URL for asset '%s' in structure cannot be empty", key)
					}
					parsedURL, err := url.ParseRequestURI(urlString)
					if err != nil {
						return fmt.Errorf("invalid URL format for asset '%s': value '%s' is not a valid URL (%w)", key, urlString, err)
					}
					// Validate Scheme: must not be empty.
					// Validate Host: must not be empty UNLESS scheme is 'file'.
					if parsedURL.Scheme == "" {
						return fmt.Errorf("invalid URL for asset '%s': value '%s' must have a scheme", key, urlString)
					}
					if parsedURL.Scheme != "file" && parsedURL.Host == "" {
						return fmt.Errorf("invalid URL for asset '%s': value '%s' (scheme '%s') must have a host", key, urlString, parsedURL.Scheme)
					}

					if strings.TrimSpace(sumString) == "" {
						return fmt.Errorf("sum for asset '%s' in structure cannot be empty", key)
					}
					matched, _ := regexp.MatchString("^[a-f0-9]{64}$", strings.ToLower(sumString))
					if !matched {
						return fmt.Errorf("invalid sum format for asset '%s': must be a 64-character SHA256 hex string", key)
					}
				} else { // Has 'url' and 'sum' keys, but types are wrong. Treat as malformed, or could be a directory with unfortunate key names.
					// For now, if it has url/sum but types are not string, consider it a subdirectory.
					// A stricter validation might error here.
					if err := validateStructureContent(v); err != nil { // Recursive call for sub-directory
						return fmt.Errorf("invalid content in sub-directory '%s' (malformed asset-like map): %w", key, err)
					}
				}
			} else { // Does not have both 'url' and 'sum' keys, treat as sub-directory
				if err := validateStructureContent(v); err != nil { // Recursive call for sub-directory
					return fmt.Errorf("invalid content in sub-directory '%s': %w", key, err)
				}
			}
		default:
			return fmt.Errorf("invalid type for '%s' in structure: expected AssetDetail (for files) or map[string]interface{} (for sub-directories/assets), got %T", key, v)
		}
	}
	return nil
}
