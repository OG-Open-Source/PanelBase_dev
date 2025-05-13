package themes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/OG-Open-Source/PanelBase/internal/configuration"
	"github.com/OG-Open-Source/PanelBase/internal/logger"
	"github.com/OG-Open-Source/PanelBase/internal/utils"
)

const (
	defaultThemeDir = "ext/themes"
	themeMetaFile   = "theme.yaml" // Standard metadata file name, changed from theme.json
)

// ActionType defines the type of installation action to take.
type ActionType int

const (
	ActionInstallNew  ActionType = iota // Install into a new directory.
	ActionOverwrite                     // Overwrite an existing directory.
	ActionErrorExists                   // Return an error because it already exists.
)

// ErrThemeAlreadyExistsNoForce is returned when a theme already exists and --force is not used.
var ErrThemeAlreadyExistsNoForce = errors.New("theme already exists and --force flag was not used")

type ThemeManager struct {
	themeDir string
	themes   map[string]*ThemeMetadata // Key: localDir (e.g., thm_abc123)
	mu       sync.RWMutex
	logger   *logger.Logger
	idGen    *utils.IDGenerator
}

func NewThemeManager(log *logger.Logger, idGen *utils.IDGenerator) (*ThemeManager, error) { // Removed themeDir ...string
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if idGen == nil {
		return nil, fmt.Errorf("IDGenerator cannot be nil")
	}
	dir := defaultThemeDir // Hardcoded path
	// log.Logf("ThemeManager using hardcoded themes directory: %s", dir) // Removed log

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create theme source directory '%s': %w", dir, err)
	}
	tm := &ThemeManager{
		themeDir: dir,
		themes:   make(map[string]*ThemeMetadata),
		logger:   log,
		idGen:    idGen,
	}
	tm.discoverThemes() // Discover themes on initialization
	return tm, nil
}

// discoverThemesLocked is the internal implementation of discovering themes.
// It assumes the caller has already acquired the necessary lock.
func (tm *ThemeManager) discoverThemesLocked() {
	tm.themes = make(map[string]*ThemeMetadata) // Reset map

	dirs, err := os.ReadDir(tm.themeDir)
	if err != nil {
		tm.logger.Logf("Error during theme discovery (reading base directory '%s'): %v", tm.themeDir, err)
		return
	}
	tm.logger.Logf("Discovering themes in '%s'...", tm.themeDir)
	indentWarn := "  " // Indent for warnings/skips within the discovery loop

	processedCount := 0
	validCount := 0
	for _, dirEntry := range dirs {
		processedCount++
		if !dirEntry.IsDir() {
			continue
		}
		themeDirPath := filepath.Join(tm.themeDir, dirEntry.Name())
		metaFilePath := filepath.Join(themeDirPath, themeMetaFile)

		if _, statErr := os.Stat(metaFilePath); os.IsNotExist(statErr) {
			// This is normal if a dir is not a theme, so no log needed unless verbose debugging.
			continue
		}
		metaData, readErr := os.ReadFile(metaFilePath)
		if readErr != nil {
			tm.logger.Logf(indentWarn+"Skipping theme directory '%s': failed to read %s: %v", dirEntry.Name(), themeMetaFile, readErr)
			continue
		}
		var meta ThemeMetadata
		unmarshalErr := json.Unmarshal(metaData, &meta)
		if unmarshalErr != nil {
			tm.logger.Logf(indentWarn+"Skipping theme directory '%s': failed to parse %s: %v", dirEntry.Name(), themeMetaFile, unmarshalErr)
			continue
		}
		if validateErr := meta.Validate(); validateErr != nil {
			tm.logger.Logf(indentWarn+"Skipping theme directory '%s': invalid metadata in %s: %v", dirEntry.Name(), themeMetaFile, validateErr)
			continue
		}
		dirNameKey := dirEntry.Name()
		if _, exists := tm.themes[dirNameKey]; exists {
			// This implies a new theme directory was added that conflicts with an existing one (by dir name)
			// or this is a re-discovery and the old entry wasn't cleared.
			// If tm.themes was cleared before calling this, this should ideally not happen unless duplicate dir names.
			tm.logger.Logf(indentWarn+"Warning: Theme with directory name '%s' already loaded. Skipping duplicate.", dirNameKey)
			continue
		}
		tm.themes[dirNameKey] = &meta
		validCount++
	}
	tm.logger.Logf("Theme discovery complete. Processed %d entries, found %d valid themes in '%s'.", processedCount, validCount, tm.themeDir)
}

// discoverThemes loads themes from the filesystem into the manager's memory.
// It reads the theme.json from each valid theme directory.
// This public method handles locking.
func (tm *ThemeManager) discoverThemes() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.discoverThemesLocked()
}

func (tm *ThemeManager) GetThemeCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return len(tm.themes)
}

// fetchThemeYAML gets the theme definition content from either a URL or a local path.
// It expects the source to point to a YAML file for theme definition.
func (tm *ThemeManager) fetchThemeYAML(source string) (yamlData []byte, parsedSourceURL *url.URL, isLocalSource bool, sourceNameForLog string, err error) {
	if u, parseErr := url.Parse(source); parseErr == nil && (u.Scheme == "http" || u.Scheme == "https") {
		isLocalSource = false
		sourceNameForLog = source
		parsedSourceURL = u
		client := http.Client{Timeout: 30 * time.Second}
		resp, httpErr := client.Get(source)
		if httpErr != nil {
			err = fmt.Errorf("failed to download theme YAML from URL '%s': %w", sourceNameForLog, httpErr)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("failed to download theme YAML from URL '%s': status %s", sourceNameForLog, resp.Status)
			return
		}
		yamlData, err = io.ReadAll(resp.Body)
		if err != nil {
			err = fmt.Errorf("failed to read theme YAML content from URL '%s': %w", sourceNameForLog, err)
			return
		}
	} else {
		isLocalSource = true
		absSourcePath, pathErr := filepath.Abs(source)
		if pathErr != nil {
			err = fmt.Errorf("failed to get absolute path for local source '%s': %w", source, pathErr)
			return
		}
		sourceNameForLog = absSourcePath
		if _, statErr := os.Stat(sourceNameForLog); os.IsNotExist(statErr) {
			err = fmt.Errorf("local theme YAML file not found at '%s'", sourceNameForLog)
			return
		}
		yamlData, err = os.ReadFile(sourceNameForLog)
		if err != nil {
			err = fmt.Errorf("failed to read local theme YAML file '%s': %w", sourceNameForLog, err)
			return
		}
	}
	return
}

// countFilesInStructure recursively counts the number of files defined in the structure map.
func countFilesInStructure(structure map[string]interface{}) int {
	count := 0
	for _, item := range structure {
		switch v := item.(type) {
		case AssetDetail: // It's a file, represented by AssetDetail
			count++
		case map[string]interface{}: // It's a subdirectory
			count += countFilesInStructure(v)
		// No default case needed here, as other types are not expected by _scanDirectoryStructure
		}
	}
	return count
}

// Old ThemeManager.InstallTheme method removed as its functionality is now covered by the package-level themes.Install function.
// downloadAndSaveStructure now takes totalFiles instead of totalSteps for clarity
func (tm *ThemeManager) downloadAndSaveStructure(baseSavePath string, currentRelativePath string, structure map[string]interface{}, baseURL *url.URL, totalFiles int, downloadedFiles *int, indentPrefix string) error {
	httpClient := http.Client{Timeout: 60 * time.Second}
	for name, item := range structure {
		currentSavePath := filepath.Join(baseSavePath, name)
		itemRelativePath := path.Join(currentRelativePath, name)
		switch v := item.(type) {
		case AssetDetail: // It's an AssetDetail struct
			asset := v
			resolvedFileURLString := asset.URL
			if baseURL != nil {
				parsedItemURL, err := url.Parse(asset.URL)
				if err != nil {
					return fmt.Errorf("error parsing asset URL '%s' for '%s': %w", asset.URL, name, err)
				}
				if !parsedItemURL.IsAbs() {
					resolvedFileURLString = baseURL.ResolveReference(parsedItemURL).String()
				}
			}

			*downloadedFiles++
			tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'...", *downloadedFiles, totalFiles, itemRelativePath)
			resp, err := httpClient.Get(resolvedFileURLString)
			if err != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
				return fmt.Errorf("failed to download file '%s' from '%s': %w", name, resolvedFileURLString, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error: status %s", *downloadedFiles, totalFiles, itemRelativePath, resp.Status)
				return fmt.Errorf("failed to download file '%s' from '%s': status %s", name, resolvedFileURLString, resp.Status)
			}

			if err := os.MkdirAll(filepath.Dir(currentSavePath), 0755); err != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error creating directory: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
				return fmt.Errorf("failed to create parent directory for '%s': %w", currentSavePath, err)
			}

			out, err := os.Create(currentSavePath)
			if err != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error creating file: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
				return fmt.Errorf("failed to create local file '%s': %w", currentSavePath, err)
			}

			// Write to file first
			_, copyErr := io.Copy(out, resp.Body)
			closeErr := out.Close() // Close the file before hashing

			if copyErr != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error saving file: %v", *downloadedFiles, totalFiles, itemRelativePath, copyErr)
				os.Remove(currentSavePath) // Attempt to clean up partially written file
				return fmt.Errorf("failed to save downloaded file '%s' to '%s': %w", name, currentSavePath, copyErr)
			}
			if closeErr != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error closing file: %v", *downloadedFiles, totalFiles, itemRelativePath, closeErr)
				// Only return closeErr if copyErr was nil, to preserve original copy error if it occurred.
				// No need to remove file here as copy was successful.
				return fmt.Errorf("failed to close local file '%s' after writing: %w", currentSavePath, closeErr)
			}

			// Now, verify checksum
			downloadedFile, err := os.Open(currentSavePath)
			if err != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Error opening downloaded file '%s' for checksum: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
				os.Remove(currentSavePath) // Clean up
				return fmt.Errorf("failed to open downloaded file '%s' for checksum: %w", currentSavePath, err)
			}
			defer downloadedFile.Close()

			hasher := sha256.New()
			if _, err := io.Copy(hasher, downloadedFile); err != nil {
				tm.logger.Logf(indentPrefix+"[%d/%d] Error hashing downloaded file '%s': %v", *downloadedFiles, totalFiles, itemRelativePath, err)
				os.Remove(currentSavePath) // Clean up
				return fmt.Errorf("failed to hash downloaded file '%s': %w", currentSavePath, err)
			}
			calculatedSum := hex.EncodeToString(hasher.Sum(nil))

			if !strings.EqualFold(calculatedSum, asset.Sum) {
				tm.logger.Logf(indentPrefix+"[%d/%d] Checksum mismatch for '%s'. Expected: %s, Got: %s", *downloadedFiles, totalFiles, itemRelativePath, asset.Sum, calculatedSum)
				os.Remove(currentSavePath) // Clean up
				return fmt.Errorf("checksum mismatch for file '%s': expected %s, got %s", name, asset.Sum, calculatedSum)
			}
			tm.logger.Logf(indentPrefix+"[%d/%d] Verified '%s'.", *downloadedFiles, totalFiles, itemRelativePath)

		case map[string]interface{}: // Value is a map, could be an AssetDetail-like map or a sub-directory
			mapItem := v
			// Try to interpret as an AssetDetail-like map first
			urlVal, urlOk := mapItem["url"]
			sumVal, sumOk := mapItem["sum"]

			if urlOk && sumOk { // It has 'url' and 'sum' keys, potentially an AssetDetail
				urlString, urlIsString := urlVal.(string)
				sumString, sumIsString := sumVal.(string)

				if urlIsString && sumIsString { // Both 'url' and 'sum' are strings, treat as AssetDetail
					// This logic is duplicated from the 'case AssetDetail:' block.
					// Consider refactoring into a helper if this becomes too unwieldy.
					resolvedFileURLString := urlString
					if baseURL != nil {
						parsedItemURL, err := url.Parse(urlString)
						if err != nil {
							return fmt.Errorf("error parsing asset URL '%s' for '%s': %w", urlString, name, err)
						}
						if !parsedItemURL.IsAbs() {
							resolvedFileURLString = baseURL.ResolveReference(parsedItemURL).String()
						}
					}

					*downloadedFiles++
					tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s' (from map)...", *downloadedFiles, totalFiles, itemRelativePath)
					resp, err := httpClient.Get(resolvedFileURLString)
					if err != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
						return fmt.Errorf("failed to download file '%s' from '%s': %w", name, resolvedFileURLString, err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error: status %s", *downloadedFiles, totalFiles, itemRelativePath, resp.Status)
						return fmt.Errorf("failed to download file '%s' from '%s': status %s", name, resolvedFileURLString, resp.Status)
					}

					if err := os.MkdirAll(filepath.Dir(currentSavePath), 0755); err != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error creating directory: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
						return fmt.Errorf("failed to create parent directory for '%s': %w", currentSavePath, err)
					}

					out, err := os.Create(currentSavePath)
					if err != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error creating file: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
						return fmt.Errorf("failed to create local file '%s': %w", currentSavePath, err)
					}

					_, copyErr := io.Copy(out, resp.Body)
					closeErr := out.Close()

					if copyErr != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error saving file: %v", *downloadedFiles, totalFiles, itemRelativePath, copyErr)
						os.Remove(currentSavePath)
						return fmt.Errorf("failed to save downloaded file '%s' to '%s': %w", name, currentSavePath, copyErr)
					}
					if closeErr != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Downloading '%s'... Error closing file: %v", *downloadedFiles, totalFiles, itemRelativePath, closeErr)
						return fmt.Errorf("failed to close local file '%s' after writing: %w", currentSavePath, closeErr)
					}

					downloadedFileToVerify, err := os.Open(currentSavePath)
					if err != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Error opening downloaded file '%s' for checksum: %v", *downloadedFiles, totalFiles, itemRelativePath, err)
						os.Remove(currentSavePath)
						return fmt.Errorf("failed to open downloaded file '%s' for checksum: %w", currentSavePath, err)
					}
					defer downloadedFileToVerify.Close()

					hasher := sha256.New()
					if _, err := io.Copy(hasher, downloadedFileToVerify); err != nil {
						tm.logger.Logf(indentPrefix+"[%d/%d] Error hashing downloaded file '%s': %v", *downloadedFiles, totalFiles, itemRelativePath, err)
						os.Remove(currentSavePath)
						return fmt.Errorf("failed to hash downloaded file '%s': %w", currentSavePath, err)
					}
					calculatedSum := hex.EncodeToString(hasher.Sum(nil))

					if !strings.EqualFold(calculatedSum, sumString) {
						tm.logger.Logf(indentPrefix+"[%d/%d] Checksum mismatch for '%s'. Expected: %s, Got: %s", *downloadedFiles, totalFiles, itemRelativePath, sumString, calculatedSum)
						os.Remove(currentSavePath)
						return fmt.Errorf("checksum mismatch for file '%s': expected %s, got %s", name, sumString, calculatedSum)
					}
					tm.logger.Logf(indentPrefix+"[%d/%d] Verified '%s' (from map).", *downloadedFiles, totalFiles, itemRelativePath)

				} else { // Has 'url' and 'sum' keys, but types are wrong. Treat as subdirectory.
					if err := os.MkdirAll(currentSavePath, 0755); err != nil {
						return fmt.Errorf("failed to create sub-directory '%s' (malformed asset-like map): %w", currentSavePath, err)
					}
					if err := tm.downloadAndSaveStructure(currentSavePath, itemRelativePath, mapItem, baseURL, totalFiles, downloadedFiles, indentPrefix); err != nil {
						return err
					}
				}
			} else { // Does not have both 'url' and 'sum' keys, treat as sub-directory
				if err := os.MkdirAll(currentSavePath, 0755); err != nil {
					return fmt.Errorf("failed to create sub-directory '%s': %w", currentSavePath, err)
				}
				if err := tm.downloadAndSaveStructure(currentSavePath, itemRelativePath, mapItem, baseURL, totalFiles, downloadedFiles, indentPrefix); err != nil {
					return err
				}
			}
		default:
			errMsg := fmt.Sprintf("unknown type in structure for key '%s': expected AssetDetail or map[string]interface{}, got %T", name, item)
			tm.logger.Logf(indentPrefix+"%s", errMsg)
			return fmt.Errorf(errMsg)
		}
	}
	return nil
}

func (tm *ThemeManager) GetTheme(name string) (*ThemeMetadata, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	theme, exists := tm.themes[name]
	return theme, exists
}

// Old ThemeManager.UpdateTheme method removed as its functionality is now covered by the package-level themes.Update function.

func (tm *ThemeManager) ListInstalledThemes() map[string]*ThemeMetadata {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	listedThemes := make(map[string]*ThemeMetadata, len(tm.themes))
	for id, meta := range tm.themes {
		// Ensure the metadata being returned includes any timestamps loaded from its local theme.json
		// The discoverThemes should already populate this correctly if theme.json has them.
		listedThemes[id] = meta
	}
	return listedThemes
}

// Old ThemeManager.RemoveTheme method removed as its functionality is now covered by the package-level themes.Remove function.
// _fetchAndValidateDefinition is a helper method to fetch, parse, and validate theme definition YAML.
func (tm *ThemeManager) _fetchAndValidateDefinition(source string) (meta *ThemeMetadata, sourceNameForLog string, parsedSourceURL *url.URL, isLocalSource bool, err error) {
	indentPrefix := "    " // This is indent2, assuming the caller used indent1 for "Fetching and validating..."
	tm.logger.Logf(indentPrefix+"Fetching definition from source '%s'...", source)
	yamlData, parsedSourceURL, isLocalSource, sourceNameForLog, fetchErr := tm.fetchThemeYAML(source)
	if fetchErr != nil {
		// fetchThemeYAML already returns a descriptive error.
		// We might want to log it here with indentation if it's not already done or if context is needed.
		// For now, assume fetchThemeYAML's error is sufficient or it logs appropriately.
		return nil, sourceNameForLog, parsedSourceURL, isLocalSource, fetchErr
	}

	tm.logger.Logf(indentPrefix+"Processing definition from '%s'...", sourceNameForLog)
	var m ThemeMetadata
	if unmarshalErr := yaml.Unmarshal(yamlData, &m); unmarshalErr != nil {
		tm.logger.Logf(indentPrefix+"Definition processing error: %v", unmarshalErr)
		tm.logger.Logf(indentPrefix+"Could not parse theme YAML from source '%s'.", sourceNameForLog) // Simplified
		return nil, sourceNameForLog, parsedSourceURL, isLocalSource, fmt.Errorf("failed to parse theme YAML from '%s': %w", sourceNameForLog, unmarshalErr)
	}

	tm.logger.Logf(indentPrefix+"DEBUG: After Unmarshal, m.SourceLink is: '%s'", m.SourceLink) // DEBUG LOG

	if validateErr := m.Validate(); validateErr != nil {
		tm.logger.Logf(indentPrefix+"Definition validation error: %v", validateErr)
		tm.logger.Logf(indentPrefix+"Invalid theme metadata from source '%s' for theme '%s'.", sourceNameForLog, m.Name) // Simplified
		return nil, sourceNameForLog, parsedSourceURL, isLocalSource, fmt.Errorf("invalid theme metadata from '%s' for theme '%s': %w", sourceNameForLog, m.Name, validateErr)
	}
	tm.logger.Logf(indentPrefix+"Metadata validated: '%s' (v%s).", m.Name, m.Version)
	return &m, sourceNameForLog, parsedSourceURL, isLocalSource, nil
}

// --- Package-Level API Functions ---

// Install downloads and installs a theme from a given source.
// It uses the provided ThemeManager instance for its operations.
func Install(tm *ThemeManager, source string, force bool) (*ThemeMetadata, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	indent1 := "  "
	indent2 := "    "

	tm.logger.Logf("Installing theme from source '%s'...", source)

	// 1. Fetch and validate the theme definition
	tm.logger.Logf(indent1 + "Fetching and validating theme definition...")
	meta, sourceNameForLog, parsedSourceURL, isLocalSource, err := tm._fetchAndValidateDefinition(source) // _fetchAndValidateDefinition will log its own sub-steps with indent2
	if err != nil {
		return nil, err
	}
	// Assuming _fetchAndValidateDefinition now logs "Metadata validated: 'Name' (vVersion)." with indent2

	// Mark as used for now
	_ = parsedSourceURL
	_ = isLocalSource
	_ = sourceNameForLog // Potentially used if _fAVD doesn't log it.

	// 2. Determine installation action based on current state
	tm.logger.Logf(indent1 + "Determining install action...")
	themesState, err := configuration.LoadThemesState()
	if err != nil {
		tm.logger.Logf(indent2+"Failed to load themes state: %v", err) // Indented error
		return nil, fmt.Errorf("failed to load themes state for theme '%s': %w", meta.Name, err)
	}

	action, targetDirName, err := tm._determineInstallAction(meta, force, themesState) // _determineInstallAction logs its internal checking logic with indent2
	if err != nil {
		return nil, err
	}
	// Log the determined action and target name from Install function's perspective
	logResolvedTargetDirName := targetDirName
	if action == ActionInstallNew && targetDirName == "" {
		logResolvedTargetDirName = "<new_id_to_be_generated>"
	}
	tm.logger.Logf(indent2+"Action determined: %v, Target ID for install: '%s'", action, logResolvedTargetDirName)

	// 3. Prepare directory
	// logTargetDirName below is for the next step's initial log message,
	// actualTargetDirName will be the definitive ID after _prepareDirectoryForInstall
	logTargetDirName := targetDirName
	if action == ActionInstallNew && targetDirName == "" {
		logTargetDirName = "<new_id_to_be_generated>"
	}
	tm.logger.Logf(indent1+"Preparing directory 'ext/themes/%s' for theme '%s'...", logTargetDirName, meta.Name)

	currentTime := time.Now().UTC().Format(time.RFC3339)
	meta.LastUpdatedAt = currentTime

	themePath, actualTargetDirName, err := tm._prepareDirectoryForInstall(targetDirName, meta, action, themesState, currentTime) // _prepareDirectoryForInstall will log its own sub-steps with indent2
	if err != nil {
		return nil, err
	}
	// Assuming _prepareDirectoryForInstall logs "Directory prepared: path (ID: id)" with indent2

	// 4. Download assets
	// _downloadThemeAssets will log "Downloading N assets..." with indent1 and per-file progress with indent2
	if err = tm._downloadThemeAssets(themePath, meta, parsedSourceURL, isLocalSource); err != nil {
		if action == ActionInstallNew {
			tm.logger.Logf(indent2+"Cleaning up directory '%s' due to download error.", themePath)
			if rmErr := os.RemoveAll(themePath); rmErr != nil {
				tm.logger.Logf(indent2+"Error during cleanup of '%s': %v", themePath, rmErr)
			}
		}
		return nil, err
	}
	// Assuming _downloadThemeAssets logs "All N assets downloaded." with indent2

	// 5. Finalizing installation
	tm.logger.Logf(indent1 + "Finalizing installation...")
	if err = tm._writeLocalThemeJSON(themePath, meta); err != nil { // _writeLocalThemeJSON will log its own sub-steps/errors with indent2
		if action == ActionInstallNew {
			tm.logger.Logf(indent2+"Cleaning up directory '%s' due to error writing local theme.json.", themePath)
			if rmErr := os.RemoveAll(themePath); rmErr != nil {
				tm.logger.Logf(indent2+"Error during cleanup of '%s': %v", themePath, rmErr)
			}
		}
		return nil, err
	}
	// Assuming _writeLocalThemeJSON logs "Local theme.json written." with indent2

	installedEntry := configuration.InstalledThemeEntry{
		ThmID:         actualTargetDirName,
		Name:          meta.Name,
		Version:       meta.Version,
		SourceLink:    meta.SourceLink,
		LastUpdatedAt: meta.LastUpdatedAt,
		InstalledAt:   meta.InstalledAt,
	}

	if err = tm._updateGlobalThemesState(actualTargetDirName, installedEntry); err != nil { // _updateGlobalThemesState will log its own sub-steps/errors with indent2
		return meta, fmt.Errorf("theme '%s' files installed/updated to '%s', but failed to save global state: %w", meta.Name, themePath, err)
	}
	// Assuming _updateGlobalThemesState logs "Global themes state updated (ID: id)." with indent2

	// 7. Discover themes to refresh in-memory cache
	tm.discoverThemesLocked()

	tm.logger.Logf("Theme '%s' (v%s) installed to '%s'.", meta.Name, meta.Version, themePath) // Final success log, no indent
	return meta, nil
}

// _updateGlobalThemesState loads the current themes state, updates a specific entry, and saves it back.
// This method assumes that necessary locks are handled by the caller if concurrent access to tm.themes cache is a concern
// immediately after this state file change (e.g., caller should lock, call this, then call discoverThemesLocked).
func (tm *ThemeManager) _updateGlobalThemesState(themeID string, entryData configuration.InstalledThemeEntry) error {
	indentPrefix := "    " // This is indent2

	currentThemesState, err := configuration.LoadThemesState()
	if err != nil {
		// Log the original error from configuration.LoadThemesState
		tm.logger.Logf(indentPrefix+"Failed to load themes state before update for theme ID '%s': %v", themeID, err)
		return fmt.Errorf("failed to load themes state before update for theme ID '%s': %w", themeID, err)
	}

	currentThemesState[themeID] = entryData

	if err := configuration.SaveThemesState(currentThemesState); err != nil {
		// Log the original error from configuration.SaveThemesState
		tm.logger.Logf(indentPrefix+"CRITICAL: Failed to save updated themes state for theme ID '%s' (Name: %s): %v. Manual correction may be needed.", themeID, entryData.Name, err)
		return fmt.Errorf("failed to save updated themes state for theme ID '%s' (Name: %s): %w", themeID, entryData.Name, err)
	}
	tm.logger.Logf(indentPrefix+"Global themes state updated for ID '%s'.", themeID)
	return nil
}

// _writeLocalThemeJSON serializes the ThemeMetadata to JSON and writes it to theme.json
// in the specified themePath.
func (tm *ThemeManager) _writeLocalThemeJSON(themePath string, meta *ThemeMetadata) error {
	indentPrefix := "    " // This is indent2
	jsonData, jsonErr := json.MarshalIndent(meta, "", "  ")
	if jsonErr != nil {
		// Log the original jsonErr, not the wrapped one for this specific log.
		tm.logger.Logf(indentPrefix+"Failed to marshal theme metadata to JSON for '%s': %v", meta.Name, jsonErr)
		return fmt.Errorf("failed to marshal theme metadata to JSON for '%s': %w", meta.Name, jsonErr)
	}

	localMetaPath := filepath.Join(themePath, "theme.json") // Explicitly use theme.json
	if err := os.WriteFile(localMetaPath, jsonData, 0644); err != nil {
		// Log the original os.WriteFile error (err), not the wrapped writeErr for this specific log.
		tm.logger.Logf(indentPrefix+"Failed to write local theme.json for theme '%s': %v", meta.Name, err)
		return fmt.Errorf("failed to write local theme.json for theme '%s' to '%s': %w", meta.Name, localMetaPath, err)
	}
	tm.logger.Logf(indentPrefix + "Local theme.json written.")
	return nil
}

// _downloadThemeAssets downloads all assets for the theme.
// It uses helper tm.countFilesInStructure and tm.downloadAndSaveStructure.
func (tm *ThemeManager) _downloadThemeAssets(themePath string, meta *ThemeMetadata, parsedSourceURL *url.URL, isLocalSource bool) error {
	indent1 := "  " // As per plan for this step under Install
	indent2 := "    "
	totalFiles := countFilesInStructure(meta.Structure)
	downloadedFiles := 0

	tm.logger.Logf(indent1+"Downloading %d assets for theme '%s' (v%s):", totalFiles, meta.Name, meta.Version)

	var baseURLForStructure *url.URL
	if !isLocalSource && parsedSourceURL != nil {
		baseURLForStructure = parsedSourceURL
	}
	// If isLocalSource is true, baseURLForStructure remains nil, and downloadAndSaveStructure should handle it
	// (e.g., by expecting absolute paths in structure or resolving relative to a base local path if applicable).
	// Current downloadAndSaveStructure resolves relative to baseURL if baseURL is not nil.
	// For local sources, the 'structure' in theme.yaml should contain relative paths from the YAML's location,
	// and fetchThemeYAML (when source is local) should ideally make these URLs absolute or provide a base for them.
	// However, current fetchThemeYAML for local source doesn't set parsedSourceURL.
	// This means downloadAndSaveStructure for local source will treat structure URLs as potentially absolute file paths or fail.
	// This needs to be consistent with how theme.yaml for local themes defines structure URLs.
	// For now, we assume downloadAndSaveStructure handles it.

	// TODO: downloadAndSaveStructure needs to accept indent2 for its per-file logs
	err := tm.downloadAndSaveStructure(themePath, "", meta.Structure, baseURLForStructure, totalFiles, &downloadedFiles, indent2)
	if err != nil {
		tm.logger.Logf(indent2+"Asset download failed for theme '%s' (v%s).", meta.Name, meta.Version)
		return fmt.Errorf("failed to download theme structure for '%s': %w", meta.Name, err)
	}

	tm.logger.Logf(indent2+"All %d assets downloaded.", totalFiles)
	return nil
}

// _prepareDirectoryForInstall handles the creation or cleanup of the theme directory.
// It sets meta.InstalledAt appropriately.
// It returns the full theme path, the actual directory name used (can be new ID), and any error.
func (tm *ThemeManager) _prepareDirectoryForInstall(
	initialTargetDirName string, // Empty for new, existing ThmID for overwrite
	meta *ThemeMetadata, // Will be modified with InstalledAt
	action ActionType,
	themesState map[string]configuration.InstalledThemeEntry, // Needed to get original InstalledAt for overwrites
	currentTime string, // Pre-calculated current time string for consistency
) (fullThemePath string, actualDirName string, err error) {
	indentPrefix := "    " // This is indent2, caller (Install) uses indent1 for "Preparing directory..."

	actualDirName = initialTargetDirName

	if action == ActionInstallNew {
		newDirID, idErr := tm.idGen.ThemeID()
		if idErr != nil {
			err = fmt.Errorf("failed to generate unique directory name for theme '%s': %w", meta.Name, idErr)
			tm.logger.Logf(indentPrefix+"Directory preparation failed for theme '%s': %v", meta.Name, err)
			return "", "", err
		}
		actualDirName = newDirID
		meta.InstalledAt = currentTime // Set InstalledAt only for brand new installations
		tm.logger.Logf(indentPrefix+"New theme installation, generated Target ID: '%s'.", actualDirName)
	} else if action == ActionOverwrite {
		// Preserve original install time if it exists from the loaded themesState
		if existingStateEntry, ok := themesState[actualDirName]; ok && existingStateEntry.InstalledAt != "" {
			meta.InstalledAt = existingStateEntry.InstalledAt
		} else {
			// Fallback if not found in state (should not happen for a known overwrite) or if InstalledAt was empty
			tm.logger.Logf(indentPrefix+"Warning: Overwriting theme '%s' (ID: %s) but original InstalledAt not found/empty in state. Setting to current time.", meta.Name, actualDirName)
			meta.InstalledAt = currentTime
		}
		tm.logger.Logf(indentPrefix+"Re-installing existing theme, Target ID: '%s'.", actualDirName)
		fullThemePath = filepath.Join(tm.themeDir, actualDirName)
		if err = os.RemoveAll(fullThemePath); err != nil {
			err = fmt.Errorf("failed to remove existing theme directory '%s' for force reinstall: %w", fullThemePath, err)
			tm.logger.Logf(indentPrefix+"Directory preparation failed for theme '%s' (cleanup error): %v", meta.Name, err)
			return "", actualDirName, err
		}
	} else {
		// Should not happen if _determineInstallAction is correct (ActionErrorExists is handled before)
		err = fmt.Errorf("unexpected action type %v in _prepareDirectoryForInstall for theme '%s'", action, meta.Name)
		tm.logger.Logf(indentPrefix+"Internal error during directory preparation for theme '%s': %v", meta.Name, err)
		return "", actualDirName, err
	}

	fullThemePath = filepath.Join(tm.themeDir, actualDirName)

	// Security check: ensure we are not operating outside the intended base directory
	absThemeDir, pathErr := filepath.Abs(tm.themeDir)
	if pathErr != nil {
		err = fmt.Errorf("could not get absolute path for base theme directory: %w", pathErr)
		tm.logger.Logf(indentPrefix+"Directory preparation failed for theme '%s' (base path error): %v", meta.Name, err)
		return "", actualDirName, err
	}
	absTargetThemePath, pathErr := filepath.Abs(fullThemePath)
	if pathErr != nil {
		err = fmt.Errorf("could not get absolute path for target theme directory '%s': %w", fullThemePath, pathErr)
		tm.logger.Logf(indentPrefix+"Directory preparation failed for theme '%s' (target path error): %v", meta.Name, err)
		return "", actualDirName, err
	}
	if !strings.HasPrefix(absTargetThemePath, absThemeDir+string(os.PathSeparator)) && absTargetThemePath != absThemeDir {
		err = fmt.Errorf("generated target directory '%s' (abs: '%s') resolves outside of base theme directory (abs: '%s')", actualDirName, absTargetThemePath, absThemeDir)
		tm.logger.Logf(indentPrefix+"Directory preparation failed for theme '%s' (security path error): %v", meta.Name, err)
		return "", actualDirName, err
	}

	if err = os.MkdirAll(fullThemePath, 0755); err != nil {
		err = fmt.Errorf("failed to create theme directory '%s': %w", fullThemePath, err)
		tm.logger.Logf(indentPrefix+"Directory preparation failed for theme '%s' (mkdir error): %v", meta.Name, err)
		return "", actualDirName, err
	}

	tm.logger.Logf(indentPrefix+"Directory prepared: '%s' (ID: '%s').", fullThemePath, actualDirName)
	return fullThemePath, actualDirName, nil
}

// _determineInstallAction checks the current themes state and decides the installation action.
// It returns the action type, the target directory name (if overwriting an existing theme), and any error.
func (tm *ThemeManager) _determineInstallAction(meta *ThemeMetadata, force bool, themesState map[string]configuration.InstalledThemeEntry) (ActionType, string, error) {
	indentPrefix := "    " // This is indent2
	existingLocalDirForExactMatch := ""
	for localDir, entry := range themesState {
		if entry.SourceLink == meta.SourceLink && entry.Version == meta.Version {
			existingLocalDirForExactMatch = localDir
			break
		}
	}

	if existingLocalDirForExactMatch != "" { // Exact match (SourceLink and Version) found
		if !force {
			tm.logger.Logf(indentPrefix+"Install aborted: Theme '%s' (v%s) version already exists (ID: %s) and force is not enabled.", meta.Name, meta.Version, existingLocalDirForExactMatch)
			return ActionErrorExists, existingLocalDirForExactMatch, ErrThemeAlreadyExistsNoForce
		}
		tm.logger.Logf(indentPrefix+"Theme '%s' (v%s) version already exists (ID: %s). Force enabled, proceeding with re-installation.", meta.Name, meta.Version, existingLocalDirForExactMatch)
		return ActionOverwrite, existingLocalDirForExactMatch, nil
	}

	var anyVersionExists bool = false
	for _, entry := range themesState {
		if entry.SourceLink == meta.SourceLink {
			anyVersionExists = true
			tm.logger.Logf(indentPrefix+"Other versions of theme '%s' (SourceLink: %s) found. Proceeding to install specified version '%s'.", meta.Name, meta.SourceLink, meta.Version)
			break
		}
	}
	if !anyVersionExists {
		tm.logger.Logf(indentPrefix+"No existing theme with SourceLink '%s' found. Proceeding with new installation for '%s' (v%s).", meta.SourceLink, meta.Name, meta.Version)
	}
	// After this function, the caller (Install) should log the determined action and targetDirName.
	return ActionInstallNew, "", nil
}

// Update checks for a newer version of an installed theme and updates it.
// It uses the provided ThemeManager instance.
func Update(tm *ThemeManager, themeID string) (*ThemeMetadata, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	indent1 := "  "
	indent2 := "    "

	// 1. Load current themes state
	themesState, err := configuration.LoadThemesState()
	if err != nil {
		tm.logger.Logf(indent1+"Failed to load themes state for theme ID '%s': %v", themeID, err)
		return nil, fmt.Errorf("failed to load themes state for update of theme ID '%s': %w", themeID, err)
	}

	// 2. Find the existing theme entry in the state
	existingStateEntry, ok := themesState[themeID]
	if !ok {
		err := fmt.Errorf("theme with ID '%s' not found in state, cannot update", themeID)
		tm.logger.Logf("Theme with ID '%s' not found in state, cannot update.", themeID) // No indent, top-level error
		return nil, err
	}

	tm.logger.Logf("Updating theme '%s' (ID: %s)...", existingStateEntry.Name, themeID)
	tm.logger.Logf(indent1+"Current version: '%s', Source: %s", existingStateEntry.Version, existingStateEntry.SourceLink)

	if existingStateEntry.SourceLink == "" {
		err := fmt.Errorf("theme '%s' (ID: %s) does not have a SourceLink, cannot update", existingStateEntry.Name, themeID)
		tm.logger.Logf(indent1 + "Theme does not have a SourceLink, cannot update.")
		return nil, err
	}

	// 3. Fetch and validate the new theme definition from source
	tm.logger.Logf(indent1 + "Fetching and validating remote definition...")
	// _fetchAndValidateDefinition logs its sub-steps with indent2
	latestMeta, _, parsedSourceURL, isLocalSource, err := tm._fetchAndValidateDefinition(existingStateEntry.SourceLink)
	if err != nil {
		tm.logger.Logf(indent1+"Failed to fetch/validate definition: %v", err) // Context at indent1
		return nil, fmt.Errorf("failed to fetch/validate definition for theme '%s' (ID: %s): %w", existingStateEntry.Name, themeID, err)
	}
	// Fetched remote definition log is now handled by _fetchAndValidateDefinition's "Metadata validated" log

	// 4. Compare versions
	tm.logger.Logf(indent1 + "Comparing versions...")
	if existingStateEntry.Version == latestMeta.Version {
		tm.logger.Logf(indent2+"Theme is already at the latest version ('%s'). No update needed.", latestMeta.Version)
		localMeta, loadLocalErr := tm._loadLocalThemeJSON(filepath.Join(tm.themeDir, themeID)) // _loadLocalThemeJSON logs with indent2 if errors occur
		if loadLocalErr != nil {
			tm.logger.Logf(indent2+"Failed to load local metadata for latest theme: %v", loadLocalErr)
			return nil, fmt.Errorf("theme is latest, but failed to reload local metadata: %w", loadLocalErr)
		}
		return localMeta, nil
	}

	if latestMeta.Version < existingStateEntry.Version {
		tm.logger.Logf(indent2+"Available version '%s' is older than installed version '%s'. No update performed.", latestMeta.Version, existingStateEntry.Version)
		localMeta, loadLocalErr := tm._loadLocalThemeJSON(filepath.Join(tm.themeDir, themeID))
		if loadLocalErr != nil {
			tm.logger.Logf(indent2+"Failed to load local metadata for theme with older remote version: %v", loadLocalErr)
			return nil, fmt.Errorf("remote version older, but failed to reload local metadata: %w", loadLocalErr)
		}
		return localMeta, nil
	}

	tm.logger.Logf(indent2+"Newer version '%s' found. Proceeding with update from '%s'.", latestMeta.Version, existingStateEntry.Version)

	// 5. Prepare directory for the new version
	currentTime := time.Now().UTC().Format(time.RFC3339)
	latestMeta.LastUpdatedAt = currentTime
	if existingStateEntry.InstalledAt != "" {
		latestMeta.InstalledAt = existingStateEntry.InstalledAt
	} else {
		tm.logger.Logf(indent2+"Warning: Existing theme (ID: %s) did not have an 'InstalledAt' time in global state. Setting to current time.", themeID)
		latestMeta.InstalledAt = currentTime
	}

	tm.logger.Logf(indent1+"Preparing local directory '%s' for update to v%s...", filepath.Join(tm.themeDir, themeID), latestMeta.Version)
	// _prepareDirectoryForInstall logs its sub-steps with indent2, including "Directory prepared..."
	themePath, _, err := tm._prepareDirectoryForInstall(themeID, latestMeta, ActionOverwrite, themesState, currentTime)
	if err != nil {
		tm.logger.Logf(indent1+"Failed to prepare directory: %v", err) // Context at indent1
		return nil, fmt.Errorf("failed to prepare directory for update of theme '%s' (ID: %s): %w", latestMeta.Name, themeID, err)
	}
	// "Directory prepared" log is now handled by _prepareDirectoryForInstall

	// 6. Download assets for the new version
	// _downloadThemeAssets logs "Downloading N assets..." with indent1, progress with indent2, and "All N assets downloaded." with indent2
	if err = tm._downloadThemeAssets(themePath, latestMeta, parsedSourceURL, isLocalSource); err != nil {
		tm.logger.Logf(indent1+"Failed to download assets: %v", err) // Context at indent1
		return nil, fmt.Errorf("failed to download assets for updated theme '%s' (ID: %s, Version: %s): %w", latestMeta.Name, themeID, latestMeta.Version, err)
	}
	// "Assets downloaded" log is now handled by _downloadThemeAssets

	// 7. Finalizing update
	tm.logger.Logf(indent1 + "Finalizing update:")
	// _writeLocalThemeJSON logs "Local theme.json written." with indent2
	if err = tm._writeLocalThemeJSON(themePath, latestMeta); err != nil {
		tm.logger.Logf(indent2+"Failed to write local theme.json: %v", err) // Context at indent2
		return nil, fmt.Errorf("failed to write local theme.json for updated theme '%s' (ID: %s, Version: %s): %w", latestMeta.Name, themeID, latestMeta.Version, err)
	}
	// "Local theme.json written" log is now handled by _writeLocalThemeJSON

	updatedGlobalEntry := configuration.InstalledThemeEntry{
		ThmID:         themeID,
		Name:          latestMeta.Name,
		Version:       latestMeta.Version,
		SourceLink:    existingStateEntry.SourceLink,
		LastUpdatedAt: latestMeta.LastUpdatedAt,
		InstalledAt:   latestMeta.InstalledAt,
	}
	// _updateGlobalThemesState logs "Global themes state updated (ID: id)." with indent2
	if err = tm._updateGlobalThemesState(themeID, updatedGlobalEntry); err != nil {
		tm.logger.Logf(indent2+"Failed to update global themes state: %v", err) // Context at indent2
		return latestMeta, fmt.Errorf("theme '%s' (ID: %s) files updated to version '%s', but failed to save global state: %w", latestMeta.Name, themeID, latestMeta.Version, err)
	}
	// "Global themes state updated" log is now handled by _updateGlobalThemesState

	// 8. Discover themes to refresh in-memory cache (was 9)
	tm.discoverThemesLocked()

	tm.logger.Logf("Theme '%s' (ID: %s) updated successfully to version '%s'.", latestMeta.Name, themeID, latestMeta.Version)
	return latestMeta, nil
}

// _loadLocalThemeJSON is a new helper to load ThemeMetadata from a local theme.json file.
// Assumes themePath is the root directory of the theme (e.g., ext/themes/thm_abc123).
func (tm *ThemeManager) _loadLocalThemeJSON(themePath string) (*ThemeMetadata, error) {
	metaFilePath := filepath.Join(themePath, "theme.json") // Explicitly use theme.json
	if _, err := os.Stat(metaFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("local theme.json not found at '%s'", metaFilePath)
	}
	metaData, err := os.ReadFile(metaFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local theme.json from '%s': %w", metaFilePath, err)
	}
	var meta ThemeMetadata
	if err = json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse local theme.json from '%s': %w", metaFilePath, err)
	}
	// Optionally, validate it, though it should be valid if written by us.
	// if err = meta.Validate(); err != nil {
	// 	return nil, fmt.Errorf("invalid metadata in local theme.json from '%s': %w", metaFilePath, err)
	// }
	return &meta, nil
}

// Remove uninstalls a theme.
// It uses the provided ThemeManager instance.
func Remove(tm *ThemeManager, themeID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	indent1 := "  "
	// indent2 will be used by helper functions

	// themeNameForLog will be fetched by _removeEntryFromGlobalState
	// We log the top-level action after successfully fetching the name.

	// 1. Remove from state file first.
	tm.logger.Logf(indent1+"Removing theme (ID: %s) from state file...", themeID)
	themeNameForLog, err := tm._removeEntryFromGlobalState(themeID) // _removeEntryFromGlobalState will log its own sub-steps/result with indent2
	if err != nil {
		// _removeEntryFromGlobalState already logs details.
		// This log provides context at indent1 level if needed, or can be removed if _rEFGS is comprehensive.
		// For now, assume _rEFGS handles its errors well.
		return err
	}
	// Top-level "Removing theme..." log after name is known
	tm.logger.Logf("Removing theme '%s' (ID: %s)...", themeNameForLog, themeID)
	// Assuming _removeEntryFromGlobalState logs "Removed from state file." with indent2

	// 2. Remove the theme directory
	tm.logger.Logf(indent1+"Deleting directory 'ext/themes/%s' for theme '%s'...", themeID, themeNameForLog)
	if err = tm._deleteThemeDirectory(themeID, themeNameForLog); err != nil { // _deleteThemeDirectory will log its own sub-steps/result with indent2
		// _deleteThemeDirectory logs specifics. This is a summary at indent1.
		tm.logger.Logf(indent1+"Directory deletion failed for theme '%s' (ID: %s). Manual cleanup may be required: %v", themeNameForLog, themeID, err)
		return fmt.Errorf("state for theme '%s' (ID: %s) removed, but directory deletion failed: %w", themeNameForLog, themeID, err)
	}
	// Assuming _deleteThemeDirectory logs "Directory deleted." with indent2

	// 3. Refresh in-memory cache
	tm.discoverThemesLocked()

	tm.logger.Logf("Theme '%s' (ID: %s) removed.", themeNameForLog, themeID) // Final success log, no indent
	return nil
}

// _removeEntryFromGlobalState removes the theme entry from the global themes.json state file.
// It returns the name of the theme that was removed (for logging) or an error.
func (tm *ThemeManager) _removeEntryFromGlobalState(themeID string) (themeName string, err error) {
	indentPrefix := "    " // This is indent2

	currentThemesState, loadErr := configuration.LoadThemesState()
	if loadErr != nil {
		err = fmt.Errorf("failed to load themes state before removing entry for theme ID '%s': %w", themeID, loadErr)
		tm.logger.Logf(indentPrefix+"Failed to load themes state before removing entry for theme ID '%s': %v", themeID, loadErr)
		return "", err
	}

	entry, exists := currentThemesState[themeID]
	if !exists {
		err = fmt.Errorf("theme with ID '%s' not found in state, cannot remove", themeID)
		tm.logger.Logf(indentPrefix+"Theme ID '%s' not found in state. Nothing to remove from state file.", themeID)
		return "", err
	}
	themeName = entry.Name

	// "Attempting to remove..." log is covered by the caller's "Removing from state file..."
	delete(currentThemesState, themeID)

	if saveErr := configuration.SaveThemesState(currentThemesState); saveErr != nil {
		// Use saveErr for logging the direct error from SaveThemesState
		tm.logger.Logf(indentPrefix+"CRITICAL: Failed to save themes state after removing entry for theme ID '%s' (Name: %s): %v. Manual correction may be needed.", themeID, themeName, saveErr)
		// Return the wrapped error for the caller
		err = fmt.Errorf("failed to save updated themes state after removing entry for theme ID '%s' (Name: %s): %w", themeID, themeName, saveErr)
		return themeName, err
	}
	tm.logger.Logf(indentPrefix+"Entry for theme '%s' (ID: %s) removed from state file.", themeName, themeID)
	return themeName, nil
}

// _deleteThemeDirectory removes the physical directory of the theme.
// It includes path security checks.
func (tm *ThemeManager) _deleteThemeDirectory(themeID string, themeNameForLog string) error {
	indentPrefix := "    " // This is indent2

	targetThemePath := filepath.Join(tm.themeDir, themeID)

	// Security check
	absThemeDir, pathErr := filepath.Abs(tm.themeDir)
	if pathErr != nil {
		err := fmt.Errorf("could not get absolute path for base theme directory: %w", pathErr)
		tm.logger.Logf(indentPrefix+"Security check failed (base path determination): %v. Directory removal skipped.", pathErr)
		return err
	}
	absTargetThemePath, pathErr := filepath.Abs(targetThemePath)
	if pathErr != nil {
		err := fmt.Errorf("could not get absolute path for target theme directory '%s': %w", targetThemePath, pathErr)
		tm.logger.Logf(indentPrefix+"Security check failed (target path determination): %v. Directory removal skipped.", pathErr)
		return err
	}
	if !strings.HasPrefix(absTargetThemePath, absThemeDir+string(os.PathSeparator)) && absTargetThemePath != absThemeDir {
		err := fmt.Errorf("security violation: target path for theme directory removal '%s' (abs: '%s') is outside the allowed base directory (abs: '%s')", targetThemePath, absTargetThemePath, absThemeDir)
		tm.logger.Logf(indentPrefix+"CRITICAL SECURITY: %v. Directory removal aborted.", err)
		return err
	}

	// "Removing directory..." log is covered by the caller's "Deleting directory..."
	if err := os.RemoveAll(targetThemePath); err != nil {
		// Log the original os.RemoveAll error
		tm.logger.Logf(indentPrefix+"Failed to delete directory '%s': %v", targetThemePath, err)
		return fmt.Errorf("failed to remove theme directory '%s' for theme '%s' (ID: %s): %w", targetThemePath, themeNameForLog, themeID, err)
	}
	tm.logger.Logf(indentPrefix+"Directory '%s' deleted.", targetThemePath)
	return nil
}

// _scanDirectoryStructure recursively scans a directory and builds the structure map
// suitable for ThemeMetadata.Structure.
// file paths in the structure map are relative to the initial rootDirForRelativePaths.
func (tm *ThemeManager) _scanDirectoryStructure(currentPath string, rootDirForRelativePaths string, baseAssetURL *url.URL) (map[string]interface{}, error) {
	structure := make(map[string]interface{})
	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory '%s': %w", currentPath, err)
	}

	for _, entry := range entries {
		entryName := entry.Name()
		fullEntryPath := filepath.Join(currentPath, entryName)

		// Calculate relative path from the root of the directory being scanned.
		// This relative path will be used with baseAssetURL to form the full URL for the asset.
		relativePath, err := filepath.Rel(rootDirForRelativePaths, fullEntryPath)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate relative path for '%s' from base '%s': %w", fullEntryPath, rootDirForRelativePaths, err)
		}
		relativePath = filepath.ToSlash(relativePath) // Ensure forward slashes for URL construction

		// Skip theme.yaml or theme.yml (or the current themeMetaFile const value)
		if !entry.IsDir() && (entryName == "theme.yaml" || entryName == "theme.yml" || entryName == themeMetaFile) {
			continue
		}
		// Skip .git, .vscode, etc. common hidden/meta directories
		if strings.HasPrefix(entryName, ".") && entryName != "." && entryName != ".." {
			continue
		}

		if entry.IsDir() {
			// Pass baseAssetURL to recursive calls
			subStructure, err := tm._scanDirectoryStructure(fullEntryPath, rootDirForRelativePaths, baseAssetURL)
			if err != nil {
				return nil, err
			}
			if len(subStructure) > 0 {
				// Key is the directory name
				structure[entryName] = subStructure
			}
		} else { // It's a file
			if baseAssetURL == nil {
				return nil, fmt.Errorf("baseAssetURL is nil, cannot resolve URL for file '%s'", entryName)
			}
			fileURL := baseAssetURL.ResolveReference(&url.URL{Path: relativePath})

			// Calculate SHA256 sum for the file
			fileData, err := os.Open(fullEntryPath)
			if err != nil {
				return nil, fmt.Errorf("failed to open file '%s' for hashing: %w", fullEntryPath, err)
			}
			defer fileData.Close()

			hasher := sha256.New()
			if _, err := io.Copy(hasher, fileData); err != nil {
				return nil, fmt.Errorf("failed to hash file '%s': %w", fullEntryPath, err)
			}
			hexSum := hex.EncodeToString(hasher.Sum(nil))

			assetDetail := AssetDetail{ // Assuming AssetDetail is defined in the same package or imported
				URL: fileURL.String(),
				Sum: hexSum,
			}
			structure[entryName] = assetDetail
		}
	}
	return structure, nil
}

// _copyThemeFiles recursively copies files from sourceDir to targetDir based on the structure map.
// The structure map's file values are relative paths from the original scan root.
func (tm *ThemeManager) _copyThemeFiles(sourceScanRootDir string, targetThemeInstallDir string, currentRelativePath string, structure map[string]interface{}) error {
	for name, item := range structure {
		targetItemPath := filepath.Join(targetThemeInstallDir, currentRelativePath, name)

		switch v := item.(type) {
		case string: // It's a file, v is the relative path from sourceScanRootDir
			sourceFilePath := filepath.Join(sourceScanRootDir, v) // v is already relative path like "js/script.js"

			// Ensure parent directory exists in target
			if err := os.MkdirAll(filepath.Dir(targetItemPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory '%s' for copying: %w", filepath.Dir(targetItemPath), err)
			}

			sourceFile, err := os.Open(sourceFilePath)
			if err != nil {
				return fmt.Errorf("failed to open source file '%s' for copying: %w", sourceFilePath, err)
			}
			defer sourceFile.Close()

			destFile, err := os.Create(targetItemPath)
			if err != nil {
				return fmt.Errorf("failed to create destination file '%s' for copying: %w", targetItemPath, err)
			}
			defer destFile.Close()

			if _, err := io.Copy(destFile, sourceFile); err != nil {
				return fmt.Errorf("failed to copy file from '%s' to '%s': %w", sourceFilePath, targetItemPath, err)
			}
			// tm.logger.Logf("Copied file '%s' to '%s'", sourceFilePath, targetItemPath) // Optional: verbose logging

		case map[string]interface{}: // It's a subdirectory
			newRelativePath := filepath.Join(currentRelativePath, name)
			// Ensure the subdirectory itself is created in the target
			if err := os.MkdirAll(targetItemPath, 0755); err != nil {
				return fmt.Errorf("failed to create subdirectory '%s' in target: %w", targetItemPath, err)
			}
			if err := tm._copyThemeFiles(sourceScanRootDir, targetThemeInstallDir, newRelativePath, v); err != nil {
				return err // Propagate error from recursive call
			}
		default:
			return fmt.Errorf("unknown type in structure for key '%s' during copy: expected string or map", name)
		}
	}
	return nil
}

// List retrieves a map of installed themes, keyed by their ID.
// It uses the provided ThemeManager instance.
// Returns InstalledThemeEntry which includes ID, name, version, and timestamps.
func List(tm *ThemeManager) (map[string]configuration.InstalledThemeEntry, error) {
	// TODO: This needs to be adapted. ListInstalledThemes returns map[string]*ThemeMetadata.
	// We need to decide the return type for the package API.
	// For now, let's return what the CLI needs, which is often from configs/themes.json.
	// The CLI 'theme list' directly loads from configs/themes.json for the summary list.
	// For detailed view, it loads local theme.json.
	// tm.logger.Logf("Package API: List called")

	// Option 1: Return the in-memory discovered themes (ThemeMetadata)
	// themesMap := tm.ListInstalledThemes() // This returns map[string]*ThemeMetadata
	// return themesMap, nil // Adjust return type if sticking to this

	// Option 2: Return the global state (InstalledThemeEntry)
	themesState, err := configuration.LoadThemesState()
	if err != nil {
		tm.logger.Logf("List: Failed to load themes state: %v", err)
		return nil, fmt.Errorf("failed to load themes state: %w", err)
	}
	return themesState, nil
	// return nil, fmt.Errorf("themes.List function not yet implemented")
}

// Create registers a new theme from a given directory path and provided metadata.
// It scans the directory for its structure, creates theme.json, copies files if necessary,
// and updates the global themes state. It uses the provided ThemeManager instance.
// Create generates a theme.yaml file in the specified directoryPath based on its content and provided metadata.
// It does not install the theme into the system or modify global state.
func Create(tm *ThemeManager, directoryPath string, name string, authors []string, version string, description string, sourceLink string) (*ThemeMetadata, error) {
	// Note: Locking is not strictly necessary for this operation as it only reads from the source
	// directory and writes one file into it, without touching shared ThemeManager state like tm.themes
	// or the global themes.json. However, if _scanDirectoryStructure or future enhancements
	// were to interact with shared state, locking would be needed. For now, it's removed for simplicity.
	// tm.mu.Lock()
	// defer tm.mu.Unlock()

	// indent1 := "  " // Standard indent for sub-steps // Commented out as no longer used

	tm.logger.Logf("Attempting to create theme manifest in directory '%s' for theme '%s'.", directoryPath, name)

	absDirectoryPath, err := filepath.Abs(directoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for source directory '%s': %w", directoryPath, err)
	}

	// 0. Parse and prepare the sourceLink to be used as the base for asset URLs in the structure
	if strings.TrimSpace(sourceLink) == "" {
		// This should ideally be caught by CLI or ThemeMetadata.Validate, but an early check is good.
		// ThemeMetadata.Validate will ensure it's a valid non-empty http/https URL later.
		return nil, fmt.Errorf("source_link cannot be empty when creating a theme manifest")
	}

	parsedBaseAssetURL, err := url.Parse(sourceLink)
	if err != nil {
		return nil, fmt.Errorf("failed to parse provided source_link '%s' as URL: %w", sourceLink, err)
	}
	if parsedBaseAssetURL.Scheme != "http" && parsedBaseAssetURL.Scheme != "https" {
		// This check is also in ThemeMetadata.Validate, but good to have here if logic changes.
		return nil, fmt.Errorf("provided source_link '%s' must use http or https scheme", sourceLink)
	}

	// Ensure parsedBaseAssetURL.Path is suitable for ResolveReference when forming asset URLs.
	// If path is empty (e.g. http://example.com), set to "/" to represent the root.
	// If path is not empty, does not end with "/", and does not appear to be a file (no "." in base name), add "/".
	if parsedBaseAssetURL.Path == "" {
		parsedBaseAssetURL.Path = "/"
	} else if !strings.HasSuffix(parsedBaseAssetURL.Path, "/") && !strings.Contains(filepath.Base(parsedBaseAssetURL.Path), ".") {
		parsedBaseAssetURL.Path += "/"
	}
	// Now parsedBaseAssetURL is ready to be used as a base for resolving relative asset paths.
	// tm.logger.Logf(indent1+"Prepared base asset URL from source_link: %s", parsedBaseAssetURL.String()) // Commented out for cleaner CLI output

	// 1. Scan the source directory structure, passing the prepared base asset URL
	structure, err := tm._scanDirectoryStructure(absDirectoryPath, absDirectoryPath, parsedBaseAssetURL)
	if err != nil {
		return nil, fmt.Errorf("failed to scan source directory structure for theme '%s' at '%s': %w", name, absDirectoryPath, err)
	}
	// tm.logger.Logf(indent1+"Scanned directory structure for theme '%s'.", name) // Commented out for cleaner CLI output

	// 2. Prepare ThemeMetadata
	// For a theme.yaml generated by 'create', InstalledAt and LastUpdatedAt are not relevant
	// as it's not yet "installed" or "updated" in the system. These fields are more for
	// the global themes.json state after an actual install/update.
	// We will omit them from the theme.yaml generated by 'create'.

	// Construct the final source_link for the theme.yaml itself.
	// It should point to where the theme.yaml would be hosted, based on the parsedBaseAssetURL.
	finalThemeManifestSourceURL := parsedBaseAssetURL.ResolveReference(&url.URL{Path: themeMetaFile}) // themeMetaFile is "theme.yaml"
	finalThemeManifestSourceLinkString := finalThemeManifestSourceURL.String()

	// Convert authors from []string to []AuthorDetail
	var authorDetails []AuthorDetail
	for _, authorName := range authors {
		// Trim space from authorName as it comes directly from user input via CLI
		trimmedName := strings.TrimSpace(authorName)
		if trimmedName != "" { // Avoid adding empty author names
			authorDetails = append(authorDetails, AuthorDetail{Name: trimmedName})
		}
	}
	// If after trimming all author names are empty, and the original authors list was also empty (or only contained whitespace)
	// the validation in types.go (len(m.Authors) == 0) should catch this if authorDetails ends up empty.
	// However, CLI `themeCreateCmd` already defaults authors to ["PanelBase Team"] if input is empty.

	newMeta := ThemeMetadata{
		Name:        name,
		Authors:     authorDetails, // Use the converted slice
		Version:     version,
		Description: description,
		SourceLink:  finalThemeManifestSourceLinkString, // Use the constructed link to theme.yaml
		Structure:   structure,
		// InstalledAt and LastUpdatedAt are omitted for a local theme.yaml
	}

	// 3. Validate metadata (this will also validate the structure against the sourceLink)
	if err = newMeta.Validate(); err != nil {
		return nil, fmt.Errorf("invalid metadata for theme manifest '%s': %w", name, err)
	}
	// tm.logger.Logf(indent1+"Validated metadata for theme '%s'.", newMeta.Name) // Commented out for cleaner CLI output

	// 4. Serialize metadata to YAML with 2-space indent
	var yamlBuffer bytes.Buffer
	encoder := yaml.NewEncoder(&yamlBuffer)
	encoder.SetIndent(2) // Set indent to 2 spaces
	err = encoder.Encode(&newMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to encode theme metadata to YAML for '%s': %w", newMeta.Name, err)
	}
	yamlData := yamlBuffer.Bytes()
	// tm.logger.Logf(indent1+"Serialized metadata to YAML for theme '%s' (with 2-space indent).", newMeta.Name) // Commented out for cleaner CLI output

	// 5. Write theme.yaml to the source directory
	manifestPath := filepath.Join(absDirectoryPath, themeMetaFile) // themeMetaFile is now "theme.yaml"
	err = os.WriteFile(manifestPath, yamlData, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write theme manifest '%s' to '%s': %w", themeMetaFile, manifestPath, err)
	}
	// tm.logger.Logf(indent1+"Theme manifest '%s' written to '%s'.", themeMetaFile, manifestPath) // Commented out for cleaner CLI output

	tm.logger.Logf("Theme manifest '%s' created successfully in directory '%s'.", themeMetaFile, absDirectoryPath)
	return &newMeta, nil
}

// GetThemeDetails retrieves the detailed metadata for a specific installed theme,
// combining information from its local theme.json and the global themes state.
func GetThemeDetails(tm *ThemeManager, themeID string) (*ThemeMetadata, *configuration.InstalledThemeEntry, error) {
	tm.mu.RLock() // Use RLock for read operations if they don't modify manager state directly
	defer tm.mu.RUnlock()

	// 1. Load global themes state to get the InstalledThemeEntry
	themesState, err := configuration.LoadThemesState()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load global themes state for theme ID '%s': %w", themeID, err)
	}

	globalEntry, exists := themesState[themeID]
	if !exists {
		// To provide a more specific error, we can check if the directory exists but is not in state.
		// However, for now, just indicate it's not in the state.
		// The CLI might do a directory check separately if needed.
		return nil, nil, fmt.Errorf("theme with ID '%s' not found in global state", themeID)
	}

	// 2. Load local theme.json for detailed ThemeMetadata
	// Construct the path to the theme's directory using tm.themeDir
	localThemePath := filepath.Join(tm.themeDir, themeID)
	localMeta, err := tm._loadLocalThemeJSON(localThemePath) // _loadLocalThemeJSON already handles "theme.json"
	if err != nil {
		// If local theme.json is missing or corrupt, but entry exists in global state, it's an inconsistency.
		return nil, &globalEntry, fmt.Errorf("theme ID '%s' found in global state, but failed to load local metadata from '%s': %w", themeID, localThemePath, err)
	}

	return localMeta, &globalEntry, nil
}
