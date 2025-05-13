package commands

import "strings"

// CommandMetadata holds metadata extracted from command script comments.
type CommandMetadata struct {
	Command      string   `meta:"command"`      // Mandatory: The command name used for execution
	PkgManagers  []string `meta:"pkg_managers"` // Mandatory: List of supported package managers (comma-separated in comment)
	Dependencies []string `meta:"dependencies"` // Mandatory: List of dependencies (comma-separated in comment, can be empty)
	Authors      []string `meta:"authors"`      // Mandatory: List of authors (comma-separated in comment)
	Version      string   `meta:"version"`      // Mandatory: Command version
	Description  string   `meta:"description"`  // Mandatory: Short description
	SourceLink   string   `meta:"source_link"`  // Mandatory: Link to the source definition
	FilePath     string   // Internal: Path to the script file
}

// IsValid checks if all mandatory metadata fields are present.
func (m *CommandMetadata) IsValid() bool {
	// Note: Dependencies can be empty, so we don't check len(m.Dependencies) > 0
	return m.Command != "" &&
		len(m.PkgManagers) > 0 && // Must support at least one pkg manager
		m.Authors != nil && // Check for nil slice, not empty content
		m.Version != "" &&
		m.Description != "" &&
		m.SourceLink != "" &&
		m.FilePath != "" // Ensure file path was set during parsing
}

// parseMetadataLine attempts to parse a single metadata line (e.g., "# @@key: value").
func parseMetadataLine(line string, meta *CommandMetadata) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "# @@") {
		return false
	}
	line = strings.TrimPrefix(line, "# @@")
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return false // Invalid format
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch key {
	case "command":
		meta.Command = value
	case "pkg_managers":
		meta.PkgManagers = splitAndTrim(value)
	case "dependencies":
		meta.Dependencies = splitAndTrim(value) // Allow empty list
	case "authors":
		meta.Authors = splitAndTrim(value)
	case "version":
		meta.Version = value
	case "description":
		meta.Description = value
	case "source_link":
		meta.SourceLink = value
	default:
		return false // Unknown key
	}
	return true
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element.
func splitAndTrim(s string) []string {
	if s == "" {
		return []string{} // Return empty slice, not nil, if input is empty
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" { // Avoid adding empty strings if there are extra commas
			result = append(result, trimmed)
		}
	}
	// If after trimming, the list is empty (e.g., input was just ","), return an empty slice.
	if len(result) == 0 {
		return []string{}
	}
	return result
}
