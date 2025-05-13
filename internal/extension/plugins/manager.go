package plugins

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OG-Open-Source/PanelBase/internal/configuration"
	"github.com/OG-Open-Source/PanelBase/internal/logger"
	"github.com/OG-Open-Source/PanelBase/internal/utils"
	"gopkg.in/yaml.v3"
)

const (
	pluginMetaFile    = "plugin.yaml"
	defaultPluginsDir = "ext/plugins"
	pluginDirPrefix   = "plg_"
	maxYAMLSize       = 1 << 20 // 1MB limit for plugin.yaml
)

// ActionType defines the type of installation action to take.
type ActionType int

const (
	ActionInstallNew  ActionType = iota // Install as a new plugin.
	ActionOverwrite                     // Overwrite an existing plugin (due to --force on exact match).
	ActionErrorExists                   // Error because the exact version already exists and --force was not used.
)

// PluginManager manages plugin installations and lifecycle.
type PluginManager struct {
	pluginDir string
	logger    *logger.Logger
	idGen     *utils.IDGenerator
	// stateFilePath string // No longer needed as path is passed to Load/Save functions
	mu sync.RWMutex
}

// NewPluginManager creates a new PluginManager.
func NewPluginManager(log *logger.Logger, idGen *utils.IDGenerator) (*PluginManager, error) { // Removed pluginDir ...string
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil for PluginManager")
	}
	if idGen == nil {
		return nil, fmt.Errorf("IDGenerator cannot be nil for PluginManager")
	}

	dir := defaultPluginsDir                                             // Hardcoded path
	log.Logf("PluginManager using hardcoded plugins directory: %s", dir) // Log the used path

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory '%s': %w", dir, err)
	}

	pm := &PluginManager{
		pluginDir: dir,
		logger:    log,
		idGen:     idGen,
	}
	// pm.discoverPlugins() // TODO: Implement plugin discovery if needed (e.g., for listing)
	return pm, nil
}

// InstallPlugin installs a plugin from a given source (URL or local path).
// It handles fetching, validation, version checking, and file placement.
func (pm *PluginManager) InstallPlugin(source string, force bool) (*PluginMetadata, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Log attempt later, after parsing metadata

	// --- 1. Fetch and Parse Metadata ---
	pm.logger.Logf("Fetching definition from source.")
	yamlData, parsedSourceURL, isLocalSource, sourceNameForLog, err := pm.fetchPluginYAML(source)
	if err != nil {
		// Log failure before returning error
		// pm.logger.Logf("Installation failed for plugin '%s'.", "unknown") // Name not available yet
		return nil, fmt.Errorf("failed to fetch plugin definition: %w", err)
	}

	var meta PluginMetadata
	pm.logger.Logf("Processing definition.")
	if err = yaml.Unmarshal(yamlData, &meta); err != nil {
		// Log failure before returning error
		pm.logger.Logf("Definition processing error: %v", err)
		pm.logger.Logf("Installation failed for plugin from source '%s'.", sourceNameForLog)
		return nil, fmt.Errorf("failed to parse plugin YAML from '%s': %w", sourceNameForLog, err)
	}
	if err = meta.Validate(); err != nil {
		// Log failure before returning error
		pm.logger.Logf("Definition processing error: %v", err)
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name) // Use name if available
		return nil, fmt.Errorf("invalid plugin metadata from '%s': %w", sourceNameForLog, err)
	}

	// Log attempt now that we have metadata
	pm.logger.Logf("Starting installation for plugin '%s' (v%s).", meta.Name, meta.Version)
	pm.logger.Logf("  Source: '%s'", sourceNameForLog)
	pm.logger.Logf("  Force installation: %v", force)

	totalFiles := countFilesInStructure(meta.Structure)
	totalSteps := totalFiles // Match log example where steps are asset downloads
	// Assets to download log moved to later, after status check

	// --- 2. Check State and Determine Action ---
	// The "Checking local status..." log will be more specific based on the outcome
	pluginsState, err := configuration.LoadPluginsState()
	if err != nil {
		// Log failure before returning error
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("failed to load plugins state: %w", err)
	}

	canonicalSourceLink := meta.SourceLink
	if canonicalSourceLink == "" {
		// Log failure before returning error
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("critical error: plugin metadata SourceLink is empty after validation (source: %s)", sourceNameForLog)
	}

	var targetDirName string = ""
	var action ActionType = ActionInstallNew
	var existingLocalDirForExactMatch string = ""

	for localDir, entry := range pluginsState {
		if entry.SourceLink == canonicalSourceLink && entry.Version == meta.Version {
			existingLocalDirForExactMatch = localDir
			break
		}
	}

	if existingLocalDirForExactMatch != "" {
		// Scenario 2 Log: Exists (same version) + install (no force)
		// Log message adjusted below based on action
		if !force {
			action = ActionErrorExists
			pm.logger.Logf("Checking local status: Plugin '%s' (v%s) already exists at '%s'. Installation aborted. To re-install this version, use --force.", meta.Name, meta.Version, filepath.Join(pm.pluginDir, existingLocalDirForExactMatch))
		} else {
			action = ActionOverwrite
			targetDirName = existingLocalDirForExactMatch
			pm.logger.Logf("Checking local status: Plugin '%s' (v%s) already exists. Force mode enabled. Proceeding with re-installation.", meta.Name, meta.Version)
		}
	} else {
		var anyVersionExists bool = false
		for _, entry := range pluginsState {
			if entry.SourceLink == canonicalSourceLink {
				anyVersionExists = true
				pm.logger.Logf("Checking local status: Other versions of plugin '%s' found. Installing specified version '%s'.", meta.Name, meta.Version)
				break
			}
		}
		if !anyVersionExists {
			pm.logger.Logf("Checking local status: Plugin '%s' (v%s) not found locally. Proceeding with new installation.", meta.Name, meta.Version)
		}
		action = ActionInstallNew
	}

	// --- 3. Handle Actions ---
	if action == ActionErrorExists {
		// Log already handled above. Return specific error without brackets.
		return nil, fmt.Errorf("plugin '%s' version '%s' already exists. Use --force to re-install this version", meta.Name, meta.Version)
	}

	// --- 4. Perform File Operations ---
	if action == ActionInstallNew {
		newDirID, idErr := pm.idGen.PluginID()
		if idErr != nil {
			// Log failure before returning error
			pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
			return nil, fmt.Errorf("failed to generate unique directory name for plugin '%s': %w", meta.Name, idErr)
		}
		targetDirName = newDirID
	}
	// If action is Overwrite, targetDirName is already set

	targetPluginPath := filepath.Join(pm.pluginDir, targetDirName)

	// Security check
	absPluginDir, err := filepath.Abs(pm.pluginDir)
	if err != nil {
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("could not get absolute path for base plugin directory: %w", err)
	}
	absTargetPluginPath, err := filepath.Abs(targetPluginPath)
	if err != nil {
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("could not get absolute path for target plugin directory: %w", err)
	}
	if !strings.HasPrefix(absTargetPluginPath, absPluginDir+string(os.PathSeparator)) && absTargetPluginPath != absPluginDir {
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("generated target directory '%s' resolves outside of base plugin directory", targetDirName)
	}

	if action == ActionOverwrite {
		// Scenario 3 Log: Exists + install --force (remove existing)
		pm.logger.Logf("Removing existing plugin directory '%s' for overwrite.", targetPluginPath)
		if err := os.RemoveAll(targetPluginPath); err != nil {
			pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
			return nil, fmt.Errorf("failed to remove existing plugin directory '%s' for force reinstall: %w", targetPluginPath, err)
		}
	}

	// Log directory creation (common for new install and overwrite)
	pm.logger.Logf("Creating plugin directory: %s", targetPluginPath) // Standard format
	if err := os.MkdirAll(targetPluginPath, 0755); err != nil {
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("failed to create plugin directory '%s': %w", targetPluginPath, err)
	}

	// --- 5. Download Structure ---
	var baseURLForStructure *url.URL
	if !isLocalSource {
		baseURLForStructure = parsedSourceURL
	}
	// totalFiles and totalSteps calculated earlier
	downloadedFiles := 0

	// Log download start (common for new install and overwrite)
	pm.logger.Logf("Downloading %d assets:", totalFiles)
	if err := pm.downloadAndSavePluginStructure(targetPluginPath, "", meta.Structure, baseURLForStructure, totalSteps, &downloadedFiles, isLocalSource); err != nil {
		// Cleanup partially downloaded files/dirs if it was a new install
		if action == ActionInstallNew {
			os.RemoveAll(targetPluginPath)
		}
		// Log failure before returning error (downloadAndSavePluginStructure logs specifics)
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("failed to download plugin structure for '%s': %w", meta.Name, err)
	}
	// Log download completion
	pm.logger.Logf("All assets downloaded successfully.")

	// --- 6. Save Metadata Locally ---
	localMetaPath := filepath.Join(targetPluginPath, pluginMetaFile)
	if err := os.WriteFile(localMetaPath, yamlData, 0644); err != nil {
		if action == ActionInstallNew {
			os.RemoveAll(targetPluginPath) // Cleanup
		}
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return nil, fmt.Errorf("failed to write local %s for plugin '%s': %w", pluginMetaFile, meta.Name, err)
	}

	// --- 7. Update State ---
	pluginsState[targetDirName] = configuration.InstalledPluginEntry{
		PlgID:      targetDirName,
		Name:       meta.Name,
		Version:    meta.Version,
		SourceLink: canonicalSourceLink,
	}
	if err := configuration.SavePluginsState(pluginsState); err != nil {
		pm.logger.Logf("CRITICAL: Plugin '%s' files installed to '%s', but FAILED TO SAVE STATE to plugins.json: %v. Manual correction may be needed.", meta.Name, targetPluginPath, err)
		// Log failure before returning error
		pm.logger.Logf("Installation failed for plugin '%s'.", meta.Name)
		return &meta, fmt.Errorf("plugin installed but failed to save state: %w", err) // Return success but with error context
	}

	// --- 8. Final Log and Return ---
	// Final success log adjusted based on action
	if action == ActionOverwrite {
		// Scenario 3 Log: Exists + install --force (final success)
		pm.logger.Logf("Plugin '%s' (v%s) (re-)installed to '%s'.", meta.Name, meta.Version, targetPluginPath)
	} else { // ActionInstallNew
		// Scenario 1 & 7 Log: (final success)
		pm.logger.Logf("Plugin '%s' (v%s) installed to '%s'.", meta.Name, meta.Version, targetPluginPath)
	}

	// Refresh internal cache (if discoverPlugins is implemented)
	// pm.discoverPlugins()

	return &meta, nil
}

// countFilesInStructure counts the number of files defined in a plugin's Structure.
// This is a simplified version, assuming only files at the current level or nested.
func countFilesInStructure(structure map[string]interface{}) int {
	count := 0
	for _, item := range structure {
		switch v := item.(type) {
		case string: // It's a file URL
			count++
		case map[string]interface{}: // It's a subdirectory
			count += countFilesInStructure(v) // Recursive call
		}
	}
	return count
}

// downloadAndSavePluginStructure downloads files and creates directories based on the plugin's Structure.
// isLocalSource: Boolean indicating if the original plugin source was a local file path.
func (pm *PluginManager) downloadAndSavePluginStructure(baseSavePath string, currentRelativePath string, structure map[string]interface{}, baseURL *url.URL, totalSteps int, downloadedFiles *int, isLocalSource bool) error {
	httpClient := http.Client{Timeout: 60 * time.Second} // Consider making timeout configurable

	// Get the absolute path of the base save directory once
	// Get the absolute path of the base save directory once
	// Get the absolute path of the base save directory once
	absBaseSavePath, pathErr := filepath.Abs(baseSavePath) // Correctly assign to pathErr
	if pathErr != nil {
		return fmt.Errorf("failed to get absolute path for base save directory '%s': %w", baseSavePath, pathErr) // Use pathErr here
	}

	for name, item := range structure {
		// Path for saving the item locally, relative to the plugin's root installation directory
		currentLocalItemSavePath := filepath.Join(baseSavePath, currentRelativePath, name)
		// Path for logging and for resolving relative URLs, always using '/'
		itemPathForLogAndURL := strings.ReplaceAll(filepath.ToSlash(filepath.Join(currentRelativePath, name)), "\\", "/")

		switch v := item.(type) {
		case string: // It's a file URL
			(*downloadedFiles)++
			pm.logger.Logf("  [%d/%d] Downloading '%s'...", *downloadedFiles, totalSteps, itemPathForLogAndURL)

			fileURLString := v
			var finalFileURL *url.URL
			var err error

			if baseURL != nil { // If plugin source was a URL, resolve relative file paths
				finalFileURL, err = baseURL.Parse(fileURLString)
				if err != nil {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error parsing relative URL: %v. Skipping.", *downloadedFiles, totalSteps, itemPathForLogAndURL, err)
					continue
				}
			} else {
				// If plugin source was local, fileURLString should be a relative or absolute path
				// For now, assume 'v' must be a resolvable URL on its own if baseURL is nil.
				// This logic might need enhancement for local YAMLs with relative file paths in structure.
				finalFileURL, err = url.ParseRequestURI(fileURLString)
				if err != nil {
					// If it's a local source and not a valid URI, it might be a local relative path.
					// This part of the logic for local-source-relative-paths in structure is not fully implemented.
					// For now, we log and skip if it's not a clear URL.
					if isLocalSource {
						pm.logger.Logf("  [%d/%d] Downloading '%s'... Path is not a URL and local relative path handling is not fully implemented. Skipping: %s", *downloadedFiles, totalSteps, itemPathForLogAndURL, fileURLString)
						continue
					}
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error parsing URL: %v. Skipping.", *downloadedFiles, totalSteps, itemPathForLogAndURL, err)
					continue
				}
			}

			if finalFileURL != nil { // Proceed with download if we have a URL
				resp, err := httpClient.Get(finalFileURL.String())
				if err != nil {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error: %v", *downloadedFiles, totalSteps, itemPathForLogAndURL, err)
					continue // Or return error based on desired strictness
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error: status %s", *downloadedFiles, totalSteps, itemPathForLogAndURL, resp.Status)
					continue // Or return error
				}

				dirPath := filepath.Dir(currentLocalItemSavePath)
				if err := os.MkdirAll(dirPath, 0755); err != nil {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error creating directory %s: %v", *downloadedFiles, totalSteps, itemPathForLogAndURL, dirPath, err)
					continue // Or return error
				}

				out, err := os.Create(currentLocalItemSavePath)
				if err != nil {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error creating file %s: %v", *downloadedFiles, totalSteps, itemPathForLogAndURL, currentLocalItemSavePath, err)
					continue // Or return error
				}

				_, copyErr := io.Copy(out, resp.Body)
				closeErr := out.Close()

				if copyErr != nil {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error saving file: %v", *downloadedFiles, totalSteps, itemPathForLogAndURL, copyErr)
					os.Remove(currentLocalItemSavePath) // Clean up partial file
					continue                            // Or return error
				}
				if closeErr != nil {
					pm.logger.Logf("  [%d/%d] Downloading '%s'... Error closing file: %v", *downloadedFiles, totalSteps, itemPathForLogAndURL, closeErr)
					// Continue even if close fails, as data is written.
				}
				pm.logger.Logf("  [%d/%d] Downloading '%s'... Done.", *downloadedFiles, totalSteps, itemPathForLogAndURL)
			} else if isLocalSource {
				// This block is for handling files when the plugin.yaml itself was loaded from a local path,
				// and the 'v' in structure is a relative path to a local file.
				// This requires knowing the directory of the original local plugin.yaml.
				// For now, this specific local-to-local copy is not fully implemented here.
				// The current fetchPluginYAML handles fetching the initial YAML.
				// If 'v' is a relative path, it should ideally be resolved against the directory of the source plugin.yaml.
				pm.logger.Logf("  [%d/%d] Downloading '%s'... Skipped (local file copy from structure not fully implemented for local plugin.yaml).", *downloadedFiles, totalSteps, itemPathForLogAndURL)
			}

		case map[string]interface{}: // It's a subdirectory
			// Security Check: Ensure the subdirectory path doesn't escape the base plugin directory
			absSubDirPath, pathErr := filepath.Abs(currentLocalItemSavePath)
			if pathErr != nil {
				pm.logger.Logf("Warning: Could not get absolute path for subdirectory '%s': %v. Skipping.", currentLocalItemSavePath, pathErr)
				continue // Skip this subdirectory
			}
			// Use the previously calculated absBaseSavePath for the check
			if !strings.HasPrefix(absSubDirPath, absBaseSavePath+string(os.PathSeparator)) && absSubDirPath != absBaseSavePath {
				pm.logger.Logf("Warning: Subdirectory path '%s' resolves outside base directory '%s'. Skipping.", currentLocalItemSavePath, absBaseSavePath)
				continue // Skip this subdirectory
			}

			pm.logger.Logf("Ensuring sub-directory '%s' exists at '%s'", name, currentLocalItemSavePath)
			if err := os.MkdirAll(currentLocalItemSavePath, 0755); err != nil {
				// Log the error but attempt to continue with other items? Or return error?
				// Returning error seems safer.
				return fmt.Errorf("failed to create sub-directory '%s': %w", currentLocalItemSavePath, err)
			}
			// Recursively process the sub-directory
			// Pass the updated relative path for logging and URL resolution
			// Pass isLocalSource down correctly in the recursive call
			if err := pm.downloadAndSavePluginStructure(baseSavePath, itemPathForLogAndURL, v, baseURL, totalSteps, downloadedFiles, isLocalSource); err != nil {
				return err // Propagate error up
			}
		default:
			return fmt.Errorf("invalid type for '%s' in plugin structure: expected string (URL/path) or map (sub-directory)", name)
		}
	}
	return nil
}

// fetchPluginYAML fetches plugin.yaml content from a URL or local path.
// Similar to ThemeManager.fetchThemeYAML but handles plugin specifics.
func (pm *PluginManager) fetchPluginYAML(source string) (yamlData []byte, parsedSourceURL *url.URL, isLocalSource bool, sourceNameForLog string, err error) {
	parsedSourceURL, urlErr := url.ParseRequestURI(source)
	if urlErr == nil && (parsedSourceURL.Scheme == "http" || parsedSourceURL.Scheme == "https") {
		// Source is a URL
		isLocalSource = false
		sourceNameForLog = parsedSourceURL.String()
		pm.logger.Logf("Fetching plugin definition from URL: %s", sourceNameForLog)

		client := http.Client{Timeout: 30 * time.Second} // Use timeout
		resp, httpErr := client.Get(sourceNameForLog)
		if httpErr != nil {
			err = fmt.Errorf("failed to fetch plugin definition from '%s': %w", sourceNameForLog, httpErr)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("failed to fetch plugin definition from '%s': status %s", sourceNameForLog, resp.Status)
			return
		}

		limitedReader := &io.LimitedReader{R: resp.Body, N: maxYAMLSize}
		yamlData, err = io.ReadAll(limitedReader)
		if err != nil {
			err = fmt.Errorf("failed to read plugin definition from '%s': %w", sourceNameForLog, err)
			return
		}
		if limitedReader.N == 0 {
			err = fmt.Errorf("plugin definition file from '%s' exceeds maximum allowed size of %d bytes", sourceNameForLog, maxYAMLSize)
			return
		}
	} else {
		// Source is potentially a local file path
		isLocalSource = true
		sourcePath := filepath.Clean(source)
		sourceNameForLog = sourcePath
		pm.logger.Logf("Reading plugin definition from local path: %s", sourceNameForLog)

		if _, statErr := os.Stat(sourcePath); os.IsNotExist(statErr) {
			err = fmt.Errorf("source '%s' is not a valid URL and file not found at local path", source)
			return
		} else if statErr != nil {
			err = fmt.Errorf("error checking local path '%s': %w", sourcePath, statErr)
			return
		}

		yamlData, err = os.ReadFile(sourcePath)
		if err != nil {
			err = fmt.Errorf("failed to read plugin definition from local path '%s': %w", sourcePath, err)
			return
		}
	}
	return // Return named variables
}

// UpdatePlugin checks for a newer version of an installed plugin and updates it.
// pluginID is the local directory name of the plugin (e.g., "plg_xyz789").
func (pm *PluginManager) UpdatePlugin(pluginID string) (*PluginMetadata, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.logger.Logf("Attempting to update plugin with ID: %s", pluginID)

	// 1. Load current plugins state
	pluginsState, err := configuration.LoadPluginsState()
	if err != nil {
		// Log failure before returning error
		pm.logger.Logf("Update failed for plugin ID '%s'.", pluginID)
		return nil, fmt.Errorf("failed to load plugins state: %w", err)
	}

	// 2. Find the installed plugin entry
	currentEntry, exists := pluginsState[pluginID]
	if !exists {
		// Scenario 4 Log: Not exists + update
		pm.logger.Logf("No versions of plugin with ID '%s' found locally. Cannot update. To install, use the install command.", pluginID)
		return nil, fmt.Errorf("plugin with ID '%s' not found in installed state", pluginID)
	}
	// Log source after finding entry
	pm.logger.Logf("  Source: '%s'", currentEntry.SourceLink)
	pm.logger.Logf("Checking local status for plugin '%s'.", currentEntry.Name) // Use name from entry

	// Scenario 5/6 Log: Exists + update (start)
	pm.logger.Logf("Latest local version of plugin '%s' is '%s' at '%s'.", currentEntry.Name, currentEntry.Version, filepath.Join(pm.pluginDir, pluginID))
	pm.logger.Logf("Fetching remote definition for plugin '%s'.", currentEntry.Name)

	// 3. Fetch the latest plugin metadata from its SourceLink
	latestYAMLData, latestParsedSourceURL, latestIsLocalSource, latestSourceNameForLog, fetchErr := pm.fetchPluginYAML(currentEntry.SourceLink)
	if fetchErr != nil {
		// Log failure before returning error
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("failed to fetch latest plugin definition from '%s' for update: %w", currentEntry.SourceLink, fetchErr)
	}

	var latestMeta PluginMetadata
	pm.logger.Logf("Processing remote definition for plugin '%s'.", currentEntry.Name)
	if err = yaml.Unmarshal(latestYAMLData, &latestMeta); err != nil {
		// Log failure before returning error
		pm.logger.Logf("Definition processing error: %v", err)
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("failed to parse latest plugin YAML from '%s' for update: %w", latestSourceNameForLog, err)
	}
	if err = latestMeta.Validate(); err != nil {
		// Log failure before returning error
		pm.logger.Logf("Definition processing error: %v", err)
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("invalid latest plugin metadata from '%s' for update: %w", latestSourceNameForLog, err)
	}
	pm.logger.Logf("  Remote version: '%s'.", latestMeta.Version) // Log remote version

	// 4. Compare versions
	// Logged above ("Latest local version...")
	if currentEntry.Version == latestMeta.Version {
		// Scenario 6 Log: Exists + update (same version)
		pm.logger.Logf("Plugin '%s' (v%s) is already the latest version. No update performed.", currentEntry.Name, currentEntry.Version)
		// Return current metadata (needs loading/parsing from local file or assume state is source of truth)
		// For simplicity, return nil, nil indicating no update occurred. Caller can check error == nil && meta == nil.
		return nil, nil
	}
	// TODO: Add semantic version comparison.

	// Scenario 5 Log: Exists + update (new version available)
	pm.logger.Logf("New version '%s' available for plugin '%s'. Current latest is '%s'.", latestMeta.Version, latestMeta.Name, currentEntry.Version)

	// 5. Perform update (overwrite existing directory)
	targetPluginPath := filepath.Join(pm.pluginDir, pluginID) // Use existing pluginID

	// Security check
	absPluginDir, err := filepath.Abs(pm.pluginDir)
	if err != nil {
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("could not get absolute path for base plugin directory: %w", err)
	}
	absTargetPluginPath, err := filepath.Abs(targetPluginPath)
	if err != nil {
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("could not get absolute path for target plugin directory: %w", err)
	}
	if !strings.HasPrefix(absTargetPluginPath, absPluginDir+string(os.PathSeparator)) && absTargetPluginPath != absPluginDir {
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("target directory '%s' for update resolves outside of base plugin directory", pluginID)
	}

	pm.logger.Logf("Removing existing plugin directory '%s' for update.", targetPluginPath)
	if err := os.RemoveAll(targetPluginPath); err != nil {
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("failed to remove existing plugin directory '%s' for update: %w", targetPluginPath, err)
	}

	pm.logger.Logf("Creating plugin directory: %s", targetPluginPath) // Log directory creation
	if err := os.MkdirAll(targetPluginPath, 0755); err != nil {
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("failed to re-create plugin directory '%s' for update: %w", targetPluginPath, err)
	}

	var baseURLForStructure *url.URL
	if !latestIsLocalSource {
		baseURLForStructure = latestParsedSourceURL
	}
	totalFiles := countFilesInStructure(latestMeta.Structure)
	totalSteps := totalFiles + 1
	downloadedFiles := 0

	// Scenario 5 Log: Downloading assets
	pm.logger.Logf("Downloading assets for plugin '%s' (v%s):", latestMeta.Name, latestMeta.Version)
	if err := pm.downloadAndSavePluginStructure(targetPluginPath, "", latestMeta.Structure, baseURLForStructure, totalSteps, &downloadedFiles, latestIsLocalSource); err != nil {
		// Log failure before returning error (downloadAndSavePluginStructure logs specifics)
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("failed to download updated plugin structure for '%s': %w. Plugin may be in a broken state.", latestMeta.Name, err)
	}
	// Scenario 5 Log: Download complete
	pm.logger.Logf("All assets downloaded for plugin '%s' (v%s).", latestMeta.Name, latestMeta.Version)

	localMetaPath := filepath.Join(targetPluginPath, pluginMetaFile)
	if err := os.WriteFile(localMetaPath, latestYAMLData, 0644); err != nil { // Save the latest YAML
		pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
		return nil, fmt.Errorf("failed to write updated local %s for plugin '%s': %w", pluginMetaFile, latestMeta.Name, err)
	}

	// 6. Update state file with new version
	pluginsState[pluginID] = configuration.InstalledPluginEntry{
		PlgID:      pluginID,
		Name:       latestMeta.Name,
		Version:    latestMeta.Version,
		SourceLink: currentEntry.SourceLink,
	}
	if err := configuration.SavePluginsState(pluginsState); err != nil {
		pm.logger.Logf("Warning: Failed to save updated plugins state after update for plugin ID '%s': %v", pluginID, err)
		// Log failure before returning error? Or just warn? Warn for now.
		// pm.logger.Logf("Update failed for plugin '%s'.", currentEntry.Name)
	}

	// Scenario 5 Log: Final success
	pm.logger.Logf("Plugin '%s' updated to v%s, installed to '%s'.", latestMeta.Name, latestMeta.Version, targetPluginPath)

	// 7. Refresh internal cache (if discoverPlugins is implemented)
	// pm.discoverPlugins()

	// Return the metadata of the newly updated plugin
	return &latestMeta, nil
}

// RemovePlugin removes an installed plugin based on its local directory ID.
func (pm *PluginManager) RemovePlugin(pluginID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.logger.Logf("Attempting to remove plugin with ID: %s", pluginID)

	// 1. Load current plugins state
	pluginsState, err := configuration.LoadPluginsState()
	if err != nil {
		// Log failure before returning error
		pm.logger.Logf("Removal failed for plugin ID '%s'.", pluginID)
		return fmt.Errorf("failed to load plugins state: %w", err)
	}

	// 2. Check if the plugin exists in the state
	entry, exists := pluginsState[pluginID]
	if !exists {
		// Log not found scenario
		pm.logger.Logf("Plugin with ID '%s' not found. No action taken.", pluginID)
		return fmt.Errorf("plugin with ID '%s' not found in installed state", pluginID)
	}
	// Get details for logging before potential errors
	pluginName := entry.Name
	pluginVersion := entry.Version

	// 3. Construct the path and perform security check
	targetPluginPath := filepath.Join(pm.pluginDir, pluginID)
	absPluginDir, err := filepath.Abs(pm.pluginDir)
	if err != nil {
		pm.logger.Logf("Removal failed for plugin '%s'.", pluginName)
		return fmt.Errorf("could not get absolute path for base plugin directory: %w", err)
	}
	absTargetPluginPath, err := filepath.Abs(targetPluginPath)
	if err != nil {
		pm.logger.Logf("Removal failed for plugin '%s'.", pluginName)
		return fmt.Errorf("could not get absolute path for target plugin directory: %w", err)
	}
	if !strings.HasPrefix(absTargetPluginPath, absPluginDir+string(os.PathSeparator)) && absTargetPluginPath != absPluginDir {
		pm.logger.Logf("Removal failed for plugin '%s'.", pluginName)
		return fmt.Errorf("target directory '%s' for removal resolves outside of base plugin directory", pluginID)
	}

	// 4. Delete the plugin directory
	pm.logger.Logf("Removing plugin '%s' (v%s) from '%s'.", pluginName, pluginVersion, targetPluginPath) // Log removal action
	if err := os.RemoveAll(targetPluginPath); err != nil {
		if os.IsNotExist(err) {
			// Directory didn't exist, which is okay for removal, but log it.
			pm.logger.Logf("Plugin directory '%s' did not exist.", targetPluginPath)
			// Continue to remove from state.
		} else {
			// Log specific removal error
			pm.logger.Logf("Removal error: %v", err)
			pm.logger.Logf("Removal failed for plugin '%s'.", pluginName)
			return fmt.Errorf("failed to remove plugin directory '%s': %w", targetPluginPath, err)
		}
	}

	// 5. Delete the entry from the state map
	delete(pluginsState, pluginID)

	// 6. Save the updated state map
	if err := configuration.SavePluginsState(pluginsState); err != nil {
		pm.logger.Logf("Warning: Plugin directory for '%s' (ID: %s) removed, but failed to save updated plugins state: %v", pluginName, pluginID, err)
		// Log failure before returning error
		pm.logger.Logf("Removal failed for plugin '%s'.", pluginName)
		return fmt.Errorf("plugin directory removed, but failed to save state: %w", err)
	}

	// Final success log
	pm.logger.Logf("Plugin '%s' (v%s) removed.", pluginName, pluginVersion)

	// 7. Refresh internal cache (if discoverPlugins is implemented)
	// pm.discoverPlugins()

	return nil
}

// ListInstalledPlugins retrieves the list of installed plugins from the state file.
func (pm *PluginManager) ListInstalledPlugins() ([]configuration.InstalledPluginEntry, error) {
	// No lock needed here as we are only reading the state file via config functions,
	// which should handle their own concurrency if necessary, or we assume CLI commands
	// are not run concurrently in a way that corrupts this read.
	// If the manager maintained an internal map populated by discovery, a RLock would be needed.

	pm.logger.Logf("Listing installed plugins from state file...")

	pluginsState, err := configuration.LoadPluginsState()
	if err != nil {
		pm.logger.Logf("Error loading plugins state for listing: %v", err)
		// Return empty list and the error
		return []configuration.InstalledPluginEntry{}, fmt.Errorf("failed to load plugins state: %w", err)
	}

	// Convert map to slice
	list := make([]configuration.InstalledPluginEntry, 0, len(pluginsState))
	for _, entry := range pluginsState {
		list = append(list, entry)
	}

	pm.logger.Logf("Found %d installed plugins in state.", len(list))
	return list, nil
}
