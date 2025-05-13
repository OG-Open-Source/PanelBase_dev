package commands

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	// "time" // Removed unused import

	"github.com/OG-Open-Source/PanelBase/internal/configuration"
	"github.com/OG-Open-Source/PanelBase/internal/logger"
	"github.com/OG-Open-Source/PanelBase/internal/utils"
)

// ActionType defines the type of installation action to take (shared concept).
// Consider moving this to a common place if used by plugins/themes too.
type ActionType int

const (
	ActionInstallNew    ActionType = iota // Install as a new command.
	ActionOverwrite                       // Overwrite an existing command (due to --force on exact match).
	ActionErrorExists                     // Error because the exact version already exists and --force was not used.
	ActionErrorConflict                   // Error because a different command exists with the same target filename.
)

const (
	// commandDirPrefix = "cmd_" // Example if commands were to be installed into unique dirs
	defaultCommandDir = "ext/commands" // Default directory for command files
	metadataPrefix    = "# @@"
	maxMetadataLines  = 20 // Limit lines to read for metadata
)

// CommandManager discovers and manages command metadata.
type CommandManager struct {
	commandDir string                      // Directory containing command files
	commands   map[string]*CommandMetadata // Map command name (from metadata) to its full metadata (including FilePath)
	mu         sync.RWMutex
	logger     *logger.Logger
	idGen      *utils.IDGenerator // Keep for potential future use (e.g., generating IDs for command instances?)
}

// NewCommandManager creates a new CommandManager instance and discovers commands from the state file.
// Requires logger and IDGenerator.
func NewCommandManager(log *logger.Logger, idGen *utils.IDGenerator) (*CommandManager, error) { // Removed commandDir ...string
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if idGen == nil {
		return nil, fmt.Errorf("IDGenerator cannot be nil")
	}

	dir := defaultCommandDir                                               // Hardcoded path
	log.Logf("CommandManager using hardcoded commands directory: %s", dir) // Log the used path

	// Ensure command source directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create command source directory '%s': %w", dir, err)
	}

	cm := &CommandManager{
		commandDir: dir,
		commands:   make(map[string]*CommandMetadata), // Initialize the map
		logger:     log,
		idGen:      idGen, // Store ID generator
	}
	cm.discoverCommands() // Discover commands on initialization
	return cm, nil
}

// discoverCommands loads command state from commands.json and populates the internal map.
// It also parses the metadata from the actual script files referenced in the state.
func (cm *CommandManager) discoverCommands() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.commands = make(map[string]*CommandMetadata) // Reset internal map

	// Load state from commands.json
	commandsState, err := configuration.LoadCommandsState()
	if err != nil {
		cm.logger.Logf("Error loading commands state: %v. Command discovery will be incomplete.", err)
		// Continue with an empty state? Or return? For now, continue.
		commandsState = make(map[string]configuration.InstalledCommandEntry)
	}

	cm.logger.Logf("Discovering commands based on state file...")
	validCount := 0
	processedFilenames := make(map[string]bool)

	for filename, entry := range commandsState {
		if processedFilenames[filename] {
			cm.logger.Logf("Warning: Duplicate filename '%s' encountered in commands state. Skipping subsequent entries.", filename)
			continue
		}
		processedFilenames[filename] = true

		filePath := filepath.Join(cm.commandDir, filename)

		// Check if the script file actually exists
		if _, statErr := os.Stat(filePath); os.IsNotExist(statErr) {
			cm.logger.Logf("Warning: Command script file '%s' referenced in state does not exist. Skipping.", filePath)
			continue
		} else if statErr != nil {
			cm.logger.Logf("Warning: Error checking command script file '%s': %v. Skipping.", filePath, statErr)
			continue
		}

		// Parse metadata from the existing script file
		meta, parseErr := parseCommandMetadata(filePath)
		if parseErr != nil {
			cm.logger.Logf("Warning: Failed to parse metadata from command script '%s': %v. Skipping.", filePath, parseErr)
			continue
		}

		// Basic validation against state entry (optional but good practice)
		if meta.Command != entry.Name || meta.Version != entry.Version || meta.SourceLink != entry.SourceLink {
			cm.logger.Logf("Warning: Metadata mismatch between state file and script file '%s' for command '%s'. State: (V: %s, SL: %s), Script: (Cmd: %s, V: %s, SL: %s). Using data from script file.",
				filename, entry.Name, entry.Version, entry.SourceLink, meta.Command, meta.Version, meta.SourceLink)
			// Decide which source of truth to use. Parsing the file seems more reliable for execution details.
		}

		// Ensure metadata is valid according to its own rules
		// We need to set FilePath before calling IsValid
		meta.FilePath = filePath
		if !meta.IsValid() {
			cm.logger.Logf("Warning: Command script '%s' has invalid metadata according to IsValid(). Skipping.", filePath)
			continue
		}

		// Check for duplicate command *names* (from metadata)
		if existingMeta, exists := cm.commands[meta.Command]; exists {
			cm.logger.Logf("Warning: Duplicate command name '%s' defined in script '%s'. It conflicts with script '%s'. Skipping '%s'.",
				meta.Command, filename, existingMeta.FilePath, filename)
			continue
		}

		// Store the full metadata (including FilePath) using the command name from metadata as the key
		cm.commands[meta.Command] = meta
		validCount++
	}

	cm.logger.Logf("Finished command discovery. Found %d valid commands.", validCount)
}

// parseCommandMetadata reads the first maxMetadataLines of a file and parses @@ metadata.
func parseCommandMetadata(filePath string) (*CommandMetadata, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	meta := &CommandMetadata{
		FilePath: filePath, // Store the file path
		// Initialize slices to avoid nil checks later if they remain empty
		PkgManagers:  []string{},
		Dependencies: []string{},
		Authors:      []string{},
	}
	foundAnyMeta := false

	// Use a LimitedReader to avoid reading huge files if metadata is missing
	// Estimate max bytes needed (generous guess: 20 lines * 200 chars/line)
	limitedReader := io.LimitReader(file, int64(maxMetadataLines*200))
	scanner := bufio.NewScanner(limitedReader)
	linesRead := 0

	for scanner.Scan() && linesRead < maxMetadataLines {
		linesRead++
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), metadataPrefix) {
			if parseMetadataLine(line, meta) {
				foundAnyMeta = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	if !foundAnyMeta {
		return nil, fmt.Errorf("no '@@' metadata found in the first %d lines", maxMetadataLines)
	}

	return meta, nil
}

// GetCommandCount returns the number of discovered and validated command files.
func (cm *CommandManager) GetCommandCount() int {
	cm.mu.RLock() // Use RLock for read-only access
	defer cm.mu.RUnlock()
	return len(cm.commands) // Return the length of the map
}

/*
// ExecuteCommand runs a specific command identified by its name.
// IMPORTANT: This needs to be refactored. It should likely take a container context,
// copy the script from meta.FilePath to the container, and execute it there.
func (cm *CommandManager) ExecuteCommand(commandName string, args ...string) ([]byte, error) {
	cm.mu.RLock()
	meta, exists := cm.commands[commandName]
	cm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("command '%s' not found or invalid", commandName)
	}

	cm.logger.Logf("Executing command '%s' from path '%s' with args: %v", commandName, meta.FilePath, args)

	// ... execution logic needs complete rewrite for container context ...
	return nil, fmt.Errorf("ExecuteCommand is currently disabled pending refactoring for container execution")
}
*/

/*
// ListCommands returns the names of all discovered and validated commands.
func (cm *CommandManager) ListCommands() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	names := make([]string, 0, len(cm.commands))
	for name := range cm.commands {
		names = append(names, name)
	}
	return names
}
*/

// InstallCommand installs a command script from a source (URL or local path).
func (cm *CommandManager) InstallCommand(source string, force bool) (*CommandMetadata, error) {
	cm.mu.Lock() // Lock for modifying state and potentially files
	defer cm.mu.Unlock()

	// --- 1. Fetch script content and parse metadata ---
	var scriptData []byte
	var err error
	var parsedSourceURL *url.URL
	var sourceNameForLog string = source

	// Check if source is a URL or a local path
	parsedSourceURL, urlErr := url.ParseRequestURI(source)
	isURL := urlErr == nil && (parsedSourceURL.Scheme == "http" || parsedSourceURL.Scheme == "https")

	if isURL {
		sourceNameForLog = parsedSourceURL.String()
	} else {
		sourcePath := filepath.Clean(source)
		sourceNameForLog = sourcePath
	}

	cm.logger.Logf("Fetching definition from source.")
	// Fetch content
	if isURL {
		resp, httpErr := http.Get(sourceNameForLog)
		if httpErr != nil {
			// Log specific download error before returning generic error?
			// cm.logger.Logf("Download error: %v", httpErr) // Example
			// cm.logger.Logf("Installation failed for command '%s'.", meta.Command) // Need meta first
			return nil, fmt.Errorf("failed to fetch command script from '%s': %w", sourceNameForLog, httpErr)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			// cm.logger.Logf("Download error: status %s", resp.Status)
			// cm.logger.Logf("Installation failed for command '%s'.", meta.Command) // Need meta first
			return nil, fmt.Errorf("failed to fetch command script from '%s': status %s", sourceNameForLog, resp.Status)
		}

		scriptData, err = io.ReadAll(resp.Body)
		if err != nil {
			// cm.logger.Logf("Download error: %v", err)
			// cm.logger.Logf("Installation failed for command '%s'.", meta.Command) // Need meta first
			return nil, fmt.Errorf("failed to read command script from '%s': %w", sourceNameForLog, err)
		}
		// Log download complete after successful read? Need meta first.
	} else {
		// Local file read
		if _, statErr := os.Stat(sourceNameForLog); os.IsNotExist(statErr) {
			return nil, fmt.Errorf("source '%s' is not a valid URL and file not found at local path", source)
		} else if statErr != nil {
			return nil, fmt.Errorf("error checking local path '%s': %w", sourceNameForLog, statErr)
		}
		scriptData, err = os.ReadFile(sourceNameForLog)
		if err != nil {
			return nil, fmt.Errorf("failed to read command script from local path '%s': %w", sourceNameForLog, err)
		}
	}

	cm.logger.Logf("Processing definition.")
	// Parse metadata *before* logging the attempt
	meta, err := parseCommandMetadataFromBytes(scriptData)
	if err != nil {
		cm.logger.Logf("Definition processing error: %v", err)
		cm.logger.Logf("Installation failed for command from source '%s'.", sourceNameForLog)
		return nil, fmt.Errorf("failed to parse command metadata from source '%s': %w", sourceNameForLog, err)
	}

	// Now log the start of installation
	cm.logger.Logf("Starting installation for command '%s' (v%s).", meta.Command, meta.Version)
	cm.logger.Logf("  Source: '%s'", sourceNameForLog)
	cm.logger.Logf("  Force installation: %v", force)

	// --- 2. Check State and Determine Action ---
	// Specific "Checking local status: ..." logs will be added below based on conditions
	commandsState, err := configuration.LoadCommandsState()
	if err != nil {
		// cm.logger.Logf("Installation failed for command '%s'.", meta.Command)
		return nil, fmt.Errorf("failed to load commands state: %w", err)
	}

	targetFilename := meta.Command + ".sh" // Simple convention
	if strings.ContainsAny(targetFilename, string(filepath.Separator)+"/\\") {
		// cm.logger.Logf("Installation failed for command '%s'.", meta.Command)
		return nil, fmt.Errorf("invalid command name '%s': cannot contain path separators", meta.Command)
	}

	var action ActionType = ActionInstallNew
	existingEntry, entryExists := commandsState[targetFilename]

	if entryExists {
		// Found entry with the target filename
		if existingEntry.SourceLink == meta.SourceLink && existingEntry.Version == meta.Version {
			// Exact match
			if !force {
				action = ActionErrorExists
				cm.logger.Logf("Checking local status: Command '%s' (v%s) already exists as '%s'. Installation aborted. To re-install this version, use --force.", meta.Command, meta.Version, targetFilename)
			} else {
				action = ActionOverwrite
				cm.logger.Logf("Checking local status: Command '%s' (v%s) already exists as '%s'. Force mode enabled. Proceeding with re-installation.", meta.Command, meta.Version, targetFilename)
			}
		} else if existingEntry.SourceLink == meta.SourceLink {
			action = ActionErrorConflict
			cm.logger.Logf("Checking local status: Conflict - Command '%s' (SourceLink: '%s') version '%s' is installed as '%s'. Cannot install version '%s' with the same filename.",
				existingEntry.Name, existingEntry.SourceLink, existingEntry.Version, targetFilename, meta.Version)
		} else {
			action = ActionErrorConflict
			cm.logger.Logf("Checking local status: Conflict - File '%s' is already used by command '%s' (SourceLink: '%s'). Cannot install command '%s' (SourceLink: '%s') with the same filename.",
				targetFilename, existingEntry.Name, existingEntry.SourceLink, meta.Command, meta.SourceLink)
		}
	} else {
		// No entry with this filename.
		action = ActionInstallNew
		cm.logger.Logf("Checking local status: Command '%s' (v%s) not found locally. Proceeding with new installation.", meta.Command, meta.Version)
	}

	// --- 3. Handle Actions ---
	if action == ActionErrorExists {
		// Log already handled above. Return specific error without brackets.
		return nil, fmt.Errorf("command '%s' version '%s' already exists. Use --force to overwrite", meta.Command, meta.Version)
	}
	if action == ActionErrorConflict {
		// Specific error messages logged above. Return specific error without brackets.
		// cm.logger.Logf("Installation failed for command '%s'.", meta.Command) // Add failure log?
		return nil, fmt.Errorf("filename conflict for '%s'. See logs for details", targetFilename)
	}

	// --- 4. Perform File Operations ---
	targetFilePath := filepath.Join(cm.commandDir, targetFilename)

	meta.FilePath = targetFilePath
	if !meta.IsValid() {
		// cm.logger.Logf("Definition processing error: Invalid metadata after setting FilePath")
		// cm.logger.Logf("Installation failed for command '%s'.", meta.Command)
		return nil, fmt.Errorf("invalid command metadata from source '%s' after setting FilePath (check mandatory fields)", sourceNameForLog)
	}

	// Security check
	absCommandDir, err := filepath.Abs(cm.commandDir)
	if err != nil {
		return nil, fmt.Errorf("could not get absolute path for base command directory: %w", err)
	}
	absTargetFilePath, err := filepath.Abs(targetFilePath)
	if err != nil {
		return nil, fmt.Errorf("could not get absolute path for target command file: %w", err)
	}
	if !strings.HasPrefix(absTargetFilePath, absCommandDir+string(os.PathSeparator)) && absTargetFilePath != absCommandDir {
		// cm.logger.Logf("Installation failed for command '%s'.", meta.Command)
		return nil, fmt.Errorf("generated target file path '%s' resolves outside of base command directory '%s'", targetFilename, cm.commandDir)
	}

	// Scenario 3 Log: Exists + install --force (download/write part)
	// No specific download logs here as it's part of "Fetching definition" for commands.
	// The writing action log below covers both new and overwrite cases.

	cm.logger.Logf("Writing command script to: %s", targetFilePath)
	err = os.WriteFile(targetFilePath, scriptData, 0755)
	if err != nil {
		// Log failure before returning error
		cm.logger.Logf("Installation failed for command '%s'.", meta.Command)
		return nil, fmt.Errorf("failed to write command script file '%s': %w", targetFilePath, err)
	}

	// --- 5. Update State ---
	commandsState[targetFilename] = configuration.InstalledCommandEntry{
		Filename:   targetFilename,
		Name:       meta.Command,
		Version:    meta.Version,
		SourceLink: meta.SourceLink,
	}

	if err := configuration.SaveCommandsState(commandsState); err != nil {
		cm.logger.Logf("CRITICAL: Command script '%s' installed, but FAILED TO SAVE STATE to commands.json: %v. Manual correction may be needed.", targetFilename, err)
		// Log failure before returning error
		cm.logger.Logf("Installation failed for command '%s'.", meta.Command)
		return meta, fmt.Errorf("command installed but failed to save state: %w", err)
	}

	// Final success log adjusted based on action
	if action == ActionOverwrite {
		// Scenario 3 Log: Exists + install --force (final success)
		cm.logger.Logf("Command '%s' re-installed to '%s'.", meta.Command, targetFilePath)
	} else { // ActionInstallNew
		// Scenario 1 Log: Not exists + install (final success)
		cm.logger.Logf("Command '%s' installed to '%s'.", meta.Command, targetFilePath)
	}

	// --- 6. Refresh Internal State ---
	cm.discoverCommands()

	// Return the metadata parsed from the script (includes FilePath implicitly via discovery)
	// Need to find the potentially updated metadata from the internal map after discovery
	cm.mu.RLock()
	finalMeta, found := cm.commands[meta.Command]
	cm.mu.RUnlock()
	if !found {
		// This shouldn't happen if discovery worked correctly after saving state
		return nil, fmt.Errorf("internal error: command '%s' installed but not found in manager after discovery", meta.Command)
	}

	return finalMeta, nil
}

// parseCommandMetadataFromBytes parses metadata from a byte slice containing script content.
// Similar to parseCommandMetadata but operates on bytes.
func parseCommandMetadataFromBytes(data []byte) (*CommandMetadata, error) {
	meta := &CommandMetadata{
		// FilePath is not set here, as it's from bytes, not a file path initially.
		// It will be set during discovery based on the state file or after determining target path.
		PkgManagers:  []string{},
		Dependencies: []string{},
		Authors:      []string{},
	}
	foundAnyMeta := false
	linesRead := 0

	// Create a reader from the byte slice
	reader := strings.NewReader(string(data))
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() && linesRead < maxMetadataLines {
		linesRead++
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), metadataPrefix) {
			if parseMetadataLine(line, meta) {
				foundAnyMeta = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning script content: %w", err)
	}

	if !foundAnyMeta {
		return nil, fmt.Errorf("no '@@' metadata found in the first %d lines of script content", maxMetadataLines)
	}

	// Basic validation (ensure command name is present, etc.)
	// The full IsValid() check requires FilePath, which isn't available here yet.
	// The caller (InstallCommand) should call meta.IsValid() after determining and setting FilePath.
	if meta.Command == "" {
		return nil, fmt.Errorf("mandatory 'command' metadata field is missing")
	}

	return meta, nil
}

// fetchCommandScript fetches command script content from a URL or local path.
// Returns script data, parsed URL (if applicable), isLocal flag, source name for logging, and error.
func (cm *CommandManager) fetchCommandScript(source string) (scriptData []byte, parsedSourceURL *url.URL, isLocalSource bool, sourceNameForLog string, err error) {
	parsedSourceURL, urlErr := url.ParseRequestURI(source)
	if urlErr == nil && (parsedSourceURL.Scheme == "http" || parsedSourceURL.Scheme == "https") {
		// Source is a URL
		isLocalSource = false
		sourceNameForLog = parsedSourceURL.String()
		cm.logger.Logf("Fetching command script from URL: %s", sourceNameForLog)

		// Consider adding http client with timeout if not already standard
		resp, httpErr := http.Get(sourceNameForLog)
		if httpErr != nil {
			err = fmt.Errorf("failed to fetch command script from '%s': %w", sourceNameForLog, httpErr)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("failed to fetch command script from '%s': status %s", sourceNameForLog, resp.Status)
			return
		}

		// Read script content (Consider adding size limit like in InstallCommand)
		scriptData, err = io.ReadAll(resp.Body)
		if err != nil {
			err = fmt.Errorf("failed to read command script from '%s': %w", sourceNameForLog, err)
			return
		}
	} else {
		// Source is potentially a local file path
		isLocalSource = true
		sourcePath := filepath.Clean(source)
		sourceNameForLog = sourcePath
		cm.logger.Logf("Reading command script from local path: %s", sourceNameForLog)

		if _, statErr := os.Stat(sourcePath); os.IsNotExist(statErr) {
			err = fmt.Errorf("source '%s' is not a valid URL and file not found at local path", source)
			return
		} else if statErr != nil {
			err = fmt.Errorf("error checking local path '%s': %w", sourcePath, statErr)
			return
		}

		scriptData, err = os.ReadFile(sourcePath)
		if err != nil {
			err = fmt.Errorf("failed to read command script from local path '%s': %w", sourcePath, err)
			return
		}
	}
	return // Return named variables
}

// UpdateCommand checks for a newer version of an installed command and updates it.
// commandName is the logical name of the command defined in its metadata (e.g., "list-users").
func (cm *CommandManager) UpdateCommand(commandName string) (*CommandMetadata, error) {
	cm.mu.Lock() // Full lock for potential state and file modification
	defer cm.mu.Unlock()

	cm.logger.Logf("Attempting to update command '%s'.", commandName)

	// 1. Find the currently installed command metadata in the manager's map
	currentMeta, exists := cm.commands[commandName]
	if !exists {
		// Scenario 4 Log: Not exists + update
		cm.logger.Logf("Command '%s' not found locally. Cannot update. To install, use the install command.", commandName)
		return nil, fmt.Errorf("command '%s' is not currently installed or managed", commandName)
	}
	// Log source after finding metadata
	cm.logger.Logf("  Source: '%s'", currentMeta.SourceLink)               // Assuming SourceLink is reliable
	cm.logger.Logf("Checking local status for command '%s'.", commandName) // Log status check

	if currentMeta.SourceLink == "" {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("command '%s' (file: %s) does not have a SourceLink defined in its metadata, cannot update", commandName, currentMeta.FilePath)
	}
	currentVersion := currentMeta.Version
	currentFilePath := currentMeta.FilePath
	currentFilename := filepath.Base(currentFilePath)

	// Scenario 5/6 Log: Exists + update (start)
	cm.logger.Logf("Command '%s' found locally. Checking for updates from source.", commandName)

	// 2. Fetch the latest command script from its SourceLink
	latestScriptData, _, _, latestSourceNameForLog, fetchErr := cm.fetchCommandScript(currentMeta.SourceLink)
	if fetchErr != nil {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("failed to fetch latest command script from '%s' for update: %w", currentMeta.SourceLink, fetchErr)
	}

	// 3. Parse metadata from the latest script content
	latestMeta, parseErr := parseCommandMetadataFromBytes(latestScriptData)
	if parseErr != nil {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("failed to parse metadata from latest command script fetched from '%s': %w", latestSourceNameForLog, parseErr)
	}
	if latestMeta.Command != commandName {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("metadata mismatch: latest script from '%s' defines command '%s', expected '%s'", latestSourceNameForLog, latestMeta.Command, commandName)
	}

	// 4. Compare versions
	cm.logger.Logf("Installed version: %s, Latest available version: %s for command '%s'", currentVersion, latestMeta.Version, commandName)
	if currentVersion == latestMeta.Version {
		// Scenario 6 Log: Exists + update (same version/file)
		cm.logger.Logf("Command '%s' is already up-to-date. No update performed.", commandName)
		return currentMeta, nil // Return current metadata, no update performed
	}
	// TODO: Add semantic version comparison.

	// Scenario 5 Log: Exists + update (new version found)
	cm.logger.Logf("New version or different file found for command '%s'.", commandName) // Simplified log
	cm.logger.Logf("Downloading update for command '%s' from '%s'.", commandName, latestSourceNameForLog)
	// Assuming fetch already happened, log completion
	cm.logger.Logf("Download complete for command '%s' update.", commandName)

	// 5. Perform update (overwrite existing file)
	targetFilePath := currentFilePath

	latestMeta.FilePath = targetFilePath
	if !latestMeta.IsValid() {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("invalid metadata in latest command script from '%s' after setting FilePath", latestSourceNameForLog)
	}

	// Security check
	absCommandDir, err := filepath.Abs(cm.commandDir)
	if err != nil {
		return nil, fmt.Errorf("could not get absolute path for base command directory: %w", err)
	}
	absTargetFilePath, err := filepath.Abs(targetFilePath)
	if err != nil {
		return nil, fmt.Errorf("could not get absolute path for target command file: %w", err)
	}
	if !strings.HasPrefix(absTargetFilePath, absCommandDir+string(os.PathSeparator)) && absTargetFilePath != absCommandDir {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("target file path '%s' for update resolves outside of base command directory '%s'", targetFilePath, cm.commandDir)
	}

	cm.logger.Logf("Overwriting command script '%s' with new version.", targetFilePath)
	err = os.WriteFile(targetFilePath, latestScriptData, 0755)
	if err != nil {
		// Log failure before returning error
		cm.logger.Logf("Update failed for command '%s'.", commandName)
		return nil, fmt.Errorf("failed to write updated command script file '%s': %w. Command may be broken.", targetFilePath, err)
	}

	// 6. Update state file with new version
	commandsState, err := configuration.LoadCommandsState()
	if err != nil {
		cm.logger.Logf("Warning: Failed to load commands state before updating version for '%s': %v. State file may become inconsistent.", commandName, err)
		commandsState = make(map[string]configuration.InstalledCommandEntry)
	}

	if entry, ok := commandsState[currentFilename]; ok {
		entry.Version = latestMeta.Version
		commandsState[currentFilename] = entry

		if err := configuration.SaveCommandsState(commandsState); err != nil {
			cm.logger.Logf("Warning: Failed to save updated commands state after update for command '%s' (file: %s): %v", commandName, currentFilename, err)
		} else {
			cm.logger.Logf("Command state for '%s' updated successfully.", commandName)
		}
	} else {
		cm.logger.Logf("Warning: Command '%s' (file: %s) was found in manager but not in state file during update. State file not updated.", commandName, currentFilename)
	}

	// Scenario 5 Log: Exists + update (final success)
	cm.logger.Logf("Command '%s' updated at '%s'.", commandName, targetFilePath) // Simplified success log

	// 7. Refresh internal cache
	cm.discoverCommands()

	// Return the newly updated and discovered metadata
	cm.mu.RLock() // Need RLock again as discoverCommands unlocks/relocks
	finalMeta, found := cm.commands[commandName]
	cm.mu.RUnlock()
	if !found {
		// This would be an unexpected internal error
		return nil, fmt.Errorf("internal error: command '%s' updated but not found in manager after rediscovery", commandName)
	}
	return finalMeta, nil
}

// TODO: Implement command update/remove/create logic.
// TODO: Implement copying commands to container environments and executing them there.

// RemoveCommand removes an installed command script and its entry from the state.
// commandName is the logical name of the command defined in its metadata.
func (cm *CommandManager) RemoveCommand(commandName string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.logger.Logf("Attempting to remove command '%s'.", commandName)

	// 1. Find the command in the internal map to get its FilePath
	currentMeta, exists := cm.commands[commandName]
	if !exists {
		// Log not found scenario
		cm.logger.Logf("Command '%s' not found. No action taken.", commandName) // Adjusted path based on name convention
		return fmt.Errorf("command '%s' not found or not currently managed", commandName)
	}
	targetFilePath := currentMeta.FilePath
	targetFilename := filepath.Base(targetFilePath)

	// 2. Load current commands state
	commandsState, err := configuration.LoadCommandsState()
	if err != nil {
		// Log failure before returning error
		cm.logger.Logf("Removal failed for command '%s'.", commandName)
		return fmt.Errorf("failed to load commands state: %w", err)
	}

	// 3. Check if the command (by filename) exists in the state
	_, stateExists := commandsState[targetFilename]
	if !stateExists {
		cm.logger.Logf("Warning: Command '%s' found in manager (file: %s), but its filename '%s' not found in state file.", commandName, targetFilePath, targetFilename)
	}

	// 4. Construct the path and perform security check
	absCommandDir, err := filepath.Abs(cm.commandDir)
	if err != nil {
		cm.logger.Logf("Removal failed for command '%s'.", commandName)
		return fmt.Errorf("could not get absolute path for base command directory: %w", err)
	}
	absTargetFilePath, err := filepath.Abs(targetFilePath)
	if err != nil {
		cm.logger.Logf("Removal failed for command '%s'.", commandName)
		return fmt.Errorf("could not get absolute path for target command file: %w", err)
	}
	if !strings.HasPrefix(absTargetFilePath, absCommandDir+string(os.PathSeparator)) && absTargetFilePath != absCommandDir {
		cm.logger.Logf("Removal failed for command '%s'.", commandName)
		return fmt.Errorf("target file path '%s' for removal resolves outside of base command directory '%s'", targetFilePath, cm.commandDir)
	}

	// 5. Delete the command script file
	cm.logger.Logf("Removing command '%s' from '%s'.", commandName, targetFilePath) // Log removal action
	if err := os.Remove(targetFilePath); err != nil {
		if os.IsNotExist(err) {
			// File didn't exist, which is okay for removal, but log it.
			cm.logger.Logf("Command file '%s' did not exist.", targetFilePath)
			// Continue to remove from state if present.
		} else {
			// Log specific removal error
			cm.logger.Logf("Removal error: %v", err)
			cm.logger.Logf("Removal failed for command '%s'.", commandName)
			return fmt.Errorf("failed to remove command script file '%s': %w", targetFilePath, err)
		}
	}

	// 6. Delete the entry from the state map (only if it existed)
	if stateExists {
		delete(commandsState, targetFilename)

		// 7. Save the updated state map
		if err := configuration.SaveCommandsState(commandsState); err != nil {
			cm.logger.Logf("Warning: Command file for '%s' removed, but failed to save updated commands state: %v", commandName, err)
			// Log failure before returning error
			cm.logger.Logf("Removal failed for command '%s'.", commandName)
			return fmt.Errorf("command file removed, but failed to save state: %w", err)
		}
		// State removal success logged as part of final success message
	} else {
		cm.logger.Logf("Command file '%s' removed (or did not exist), state file was not modified as entry was missing.", targetFilePath)
	}

	// Final success log
	cm.logger.Logf("Command '%s' removed.", commandName)

	// 8. Refresh internal cache
	cm.discoverCommands()

	return nil
}

// ListInstalledCommands returns a slice of metadata for all discovered and valid commands.
func (cm *CommandManager) ListInstalledCommands() []*CommandMetadata {
	cm.mu.RLock() // Use RLock for read-only access to the map
	defer cm.mu.RUnlock()

	cm.logger.Logf("Listing installed commands from internal map...")

	list := make([]*CommandMetadata, 0, len(cm.commands))
	for _, meta := range cm.commands {
		list = append(list, meta)
	}

	cm.logger.Logf("Found %d installed commands.", len(list))
	return list
}
