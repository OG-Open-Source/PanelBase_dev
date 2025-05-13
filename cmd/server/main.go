package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	// "text/tabwriter" // No longer needed for Markdown style

	"github.com/OG-Open-Source/PanelBase/internal/configuration"
	"github.com/OG-Open-Source/PanelBase/internal/container"
	"github.com/OG-Open-Source/PanelBase/internal/extension/commands"
	"github.com/OG-Open-Source/PanelBase/internal/extension/plugins"
	"github.com/OG-Open-Source/PanelBase/internal/extension/themes"
	"github.com/OG-Open-Source/PanelBase/internal/logger"
	"github.com/OG-Open-Source/PanelBase/internal/rpc"
	"github.com/OG-Open-Source/PanelBase/internal/utils"
	"github.com/spf13/cobra"
)

var (
	// Used for flags.
	// cfgFile string // Example flag definition

	rootCmd = &cobra.Command{
		Use:   "panelbase",
		Short: "PanelBase: A modular server management panel", // Keeping Short description as is
		Long: `PanelBase is a powerful, modular server management panel.
Manage application containers, customize with themes and plugins,
and execute custom commands with ease.`,
		Example:           "",                                               // Examples removed from root help as per user request
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}, // Disable the default completion command
	}
)

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}

// calculateDisplayWidth calculates the visual width of a string,
// considering CJK characters as 2 units wide and others as 1.
func calculateDisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			width += 2
		} else {
			width += 1
		}
	}
	return width
}

// truncateStringToDisplayWidth truncates a string to a maximum display width,
// appending "..." if truncated. CJK characters are considered 2 units wide.
func truncateStringToDisplayWidth(s string, maxWidth int) string {
	currentWidth := 0
	ellipsisWidth := calculateDisplayWidth("...")
	if maxWidth <= ellipsisWidth { // Not enough space for ellipsis
		// Try to return at least one char if possible, or empty
		if maxWidth > 0 && len(s) > 0 {
			return string([]rune(s)[0])
		}
		return ""
	}

	runes := []rune(s)
	for i, r := range runes {
		charWidth := 1
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			charWidth = 2
		}

		if currentWidth+charWidth > maxWidth {
			// Need to truncate and potentially add ellipsis
			// We need to ensure the string part + ellipsis fits
			// Iterate backwards from current position to make space for ellipsis
			tempWidth := 0
			j := 0
			for ; j < i; j++ {
				r_j := runes[j]
				w_j := 1
				if unicode.Is(unicode.Han, r_j) || unicode.Is(unicode.Hangul, r_j) || unicode.Is(unicode.Hiragana, r_j) || unicode.Is(unicode.Katakana, r_j) {
					w_j = 2
				}
				if tempWidth+w_j+ellipsisWidth > maxWidth {
					break // Found the cut-off point
				}
				tempWidth += w_j
			}
			return string(runes[:j]) + "..."
		}
		currentWidth += charWidth
		if currentWidth == maxWidth && i < len(runes)-1 { // Exact match but more chars exist
			// Similar logic to above, make space for ellipsis
			tempWidth := 0
			j := 0
			for ; j <= i; j++ { // Iterate up to current char
				r_j := runes[j]
				w_j := 1
				if unicode.Is(unicode.Han, r_j) || unicode.Is(unicode.Hangul, r_j) || unicode.Is(unicode.Hiragana, r_j) || unicode.Is(unicode.Katakana, r_j) {
					w_j = 2
				}
				if tempWidth+w_j+ellipsisWidth > maxWidth {
					j-- // step back one char
					break
				}
				tempWidth += w_j
			}
			if j < 0 {
				j = 0
			} // Ensure j is not negative
			return string(runes[:j+1]) + "..."
		}
	}
	return s // No truncation needed
}

// scanDirectoryStructure has been moved to internal/extension/themes/manager.go as a private method.

// transformStructurePathsToURLs was defined but its logic was integrated into themeCreateCmd's Run.
// Removing the unused standalone function.

func main() {
	if err := Execute(); err != nil {
		// Check if the error is our specific sentinel error
		if errors.Is(err, themes.ErrThemeAlreadyExistsNoForce) {
			// InstallTheme already logged the detailed message.
			// We just need to ensure the CLI exits with an error code.
			os.Exit(1)
		} else {
			// For all other errors, print them to stderr and exit.
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.panelbase.yaml)")

	// Add commands to root command
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(themeCmd)
	rootCmd.AddCommand(pluginCmd)    // Add plugin command here
	rootCmd.AddCommand(commandCmd)   // Add command command here
	rootCmd.AddCommand(containerCmd) // Add container command here

	// Hide the default help command from the list of available commands
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
}

// --- Server Command ---
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the PanelBase server process",
	Long:  `Commands related to controlling the main PanelBase server process.`,
}

func init() {
	serverCmd.AddCommand(serverStartCmd)
}

// func init() { // Moved AddCommand to rootCmd init
// 	serverCmd.AddCommand(serverStartCmd)
// }

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the PanelBase server",
	Long: `Initializes all managers (Containers, Plugins, Themes, Commands)
and starts the core PanelBase RPC server in the foreground.
The server will continue running until manually stopped (e.g., Ctrl+C).`,
	Example: `  panelbase server start`,
	Run: func(cmd *cobra.Command, args []string) {
		startPanelBaseServer(cmd, args)
	},
}

// --- Theme Command ---
var themeCmd = &cobra.Command{
	Use:   "themes", // Changed from "theme" to "themes"
	Short: "Manage themes",
	Long:  `Commands for installing, listing, and managing themes.`,
}

var themeInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a theme from a URL or local path",
	Long: `Downloads and installs a theme from the specified source.
The source must be either a direct URL pointing to a 'theme.yaml' file
or a local filesystem path to a 'theme.yaml' file.

The command will:
1. Fetch and validate the 'theme.yaml' metadata.
2. Create a directory for the theme under 'ext/themes/'.
3. Download all files specified in the 'structure' section of the metadata.
4. Save the original 'theme.yaml' alongside the downloaded files.`,
	Example: `  panelbase theme install https://example.com/path/to/mytheme.yaml
  panelbase theme install /path/to/local/theme.yaml
  panelbase theme install ./my_local_theme.yaml --force`,
	Args: cobra.ExactArgs(1), // Requires exactly one argument: the source
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		force, _ := cmd.Flags().GetBool("force") // Get the value of the --force flag

		// For CLI commands, we primarily use fmt for output, but initialize logger for manager dependencies.
		appLogger, err := logger.NewLogger() // Use standard logger initialization
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize logger for CLI: %v\n", err)
			os.Exit(1)
		}
		defer appLogger.Close() // Still good practice to close it

		// Initialize ID Generator (needed for ThemeManager)
		cfgForInstall, errCfg := configuration.LoadConfig() // Load config to get security settings
		if errCfg != nil {
			// appLogger.Logf("Failed to load configuration for theme install: %v", errCfg) // Removed CLI layer log
			fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", errCfg)
			os.Exit(1)
		}
		idGenForInstall, errIDGen := utils.NewIDGenerator(&cfgForInstall.Security)
		if errIDGen != nil {
			// appLogger.Logf("Failed to initialize ID generator for theme install: %v", errIDGen) // Removed CLI layer log
			fmt.Fprintf(os.Stderr, "Failed to initialize ID generator: %v\n", errIDGen)
			os.Exit(1)
		}

		// Initialize only necessary managers for this command
		themeMgr, err := themes.NewThemeManager(appLogger, idGenForInstall) // Pass idGen
		if err != nil {
			// appLogger.Logf("Failed to initialize Theme Manager for install: %v", err) // Removed CLI layer log
			fmt.Fprintf(os.Stderr, "Failed to initialize Theme Manager: %v\n", err) // User-facing error
			os.Exit(1)
		}

		// Call the new package-level Install function
		_, err = themes.Install(themeMgr, source, force) // meta is no longer needed here
		if err != nil {
			// Check if it's the specific error that themes.Install might return (and logs internally)
			if errors.Is(err, themes.ErrThemeAlreadyExistsNoForce) {
				// themes.Install already logs this specific scenario.
				// CLI just needs to exit with an error code.
				os.Exit(1)
			} else {
				// For other errors, print to stderr. Detailed logging is in themes package.
				// appLogger.Logf("Error installing theme: %v", err) // Removed CLI layer log
				fmt.Fprintf(os.Stderr, "Error installing theme: %v\n", err)
				os.Exit(1)
			}
		}
		// Success message is now handled by the themes package logging.
	},
}

var themeListCmd = &cobra.Command{
	Use:   "list [theme_id]",
	Short: "Lists all installed themes or details of a specific theme",
	Long: `Displays a list of all themes currently installed and registered in the global state file (configs/themes.json).
If a theme_id is provided, it displays detailed information for that specific theme from its local theme.json file, presented vertically.`,
	Example: `  panelbase theme list
  panelbase theme list thm_J4yoW1B5kDzy`,
	Args: cobra.MaximumNArgs(1), // 0 or 1 argument
	Run: func(cmd *cobra.Command, args []string) {
		appLogger, _, idGen := initBaseForCLI() // idGen is needed for NewThemeManager
		defer appLogger.Close()

		if len(args) == 0 {
			// --- List all themes (horizontal) ---
			themeMgr, err := themes.NewThemeManager(appLogger, idGen)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize Theme Manager: %v\n", err)
				os.Exit(1)
			}

			themesState, err := themes.List(themeMgr) // Call new package-level List function
			if err != nil {
				// themes.List logs details, CLI just shows user-facing error
				fmt.Fprintf(os.Stderr, "Error listing themes: %v\n", err)
				os.Exit(1)
			}

			if len(themesState) == 0 {
				fmt.Println("No themes installed.")
				return
			}

			headers := []string{"ID", "NAME", "VERSION", "INSTALLED AT", "LAST UPDATED"}
			columnWidths := make([]int, len(headers))

			for i, h := range headers {
				columnWidths[i] = calculateDisplayWidth(h)
			}

			type themeRow struct {
				ID          string
				Name        string // Truncated name
				Version     string
				InstalledAt string
				LastUpdated string
			}
			var rows []themeRow

			// Iterate over themesState (map[string]configuration.InstalledThemeEntry)
			for themeID, entry := range themesState {
				installedAtStr := entry.InstalledAt
				if installedAtStr == "" {
					installedAtStr = "N/A"
				}
				lastUpdatedStr := entry.LastUpdatedAt
				if lastUpdatedStr == "" {
					lastUpdatedStr = "N/A"
				}

				displayName := truncateStringToDisplayWidth(entry.Name, 15)
				row := themeRow{themeID, displayName, entry.Version, installedAtStr, lastUpdatedStr}
				rows = append(rows, row)

				if idWidth := calculateDisplayWidth(row.ID); idWidth > columnWidths[0] {
					columnWidths[0] = idWidth
				}
				if nameContentWidth := calculateDisplayWidth(row.Name); nameContentWidth > columnWidths[1] {
					columnWidths[1] = nameContentWidth
				}
				if versionWidth := calculateDisplayWidth(row.Version); versionWidth > columnWidths[2] {
					columnWidths[2] = versionWidth
				}
				if installedAtWidth := calculateDisplayWidth(row.InstalledAt); installedAtWidth > columnWidths[3] {
					columnWidths[3] = installedAtWidth
				}
				if lastUpdatedWidth := calculateDisplayWidth(row.LastUpdated); lastUpdatedWidth > columnWidths[4] {
					columnWidths[4] = lastUpdatedWidth
				}
			}

			for i, h := range headers {
				fmt.Print(h)
				padding := columnWidths[i] - calculateDisplayWidth(h)
				if padding > 0 {
					fmt.Print(strings.Repeat(" ", padding))
				}
				if i < len(headers)-1 {
					fmt.Print("  ")
				}
			}
			fmt.Println()

			for _, row := range rows {
				cells := []string{row.ID, row.Name, row.Version, row.InstalledAt, row.LastUpdated}
				for i, cell := range cells {
					fmt.Print(cell)
					padding := columnWidths[i] - calculateDisplayWidth(cell)
					if padding > 0 {
						fmt.Print(strings.Repeat(" ", padding))
					}
					if i < len(cells)-1 {
						fmt.Print("  ")
					}
				}
				fmt.Println()
			}
		} else {
			// --- List specific theme details (vertical) ---
			themeID := args[0]

			// Initialize ThemeManager (appLogger and idGen are already initialized at the start of Run)
			themeMgr, err := themes.NewThemeManager(appLogger, idGen)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize Theme Manager: %v\n", err)
				os.Exit(1)
			}

			localMeta, globalEntry, err := themes.GetThemeDetails(themeMgr, themeID)
			if err != nil {
				// themes.GetThemeDetails should provide a descriptive error
				fmt.Fprintf(os.Stderr, "Error getting theme details for '%s': %v\n", themeID, err)
				os.Exit(1)
			}

			fmt.Printf("%-15s: %s\n", "ID", themeID)
			if localMeta != nil {
				fmt.Printf("%-15s: %s\n", "NAME", localMeta.Name)
				var authorNames []string
				for _, author := range localMeta.Authors {
					authorNames = append(authorNames, author.Name)
				}
				fmt.Printf("%-15s: %s\n", "AUTHORS", strings.Join(authorNames, ", "))
				fmt.Printf("%-15s: %s\n", "VERSION", localMeta.Version)
				fmt.Printf("%-15s: %s\n", "DESCRIPTION", localMeta.Description)
				fmt.Printf("%-15s: %s\n", "SOURCE_LINK", localMeta.SourceLink) // This is from local theme.json

				structureJSON, jsonErr := json.MarshalIndent(localMeta.Structure, "", "  ")
				if jsonErr != nil {
					fmt.Printf("%-15s: Error marshalling structure: %v\n", "STRUCTURE", jsonErr)
				} else {
					fmt.Printf("%-15s:\n%s\n", "STRUCTURE", string(structureJSON))
				}
			} else {
				// This case should ideally be handled by GetThemeDetails returning an error if localMeta is nil.
				// If globalEntry is not nil, we can print its name at least.
				if globalEntry != nil {
					fmt.Printf("%-15s: %s (from global state)\n", "NAME", globalEntry.Name)
					fmt.Printf("%-15s: %s\n", "VERSION", globalEntry.Version)
					fmt.Printf("%-15s: %s (from global state)\n", "SOURCE_LINK", globalEntry.SourceLink)
				} else {
					// This implies GetThemeDetails returned no error, but also no data, which is unexpected.
					// Or, themeID was not found at all (which GetThemeDetails should error on).
					fmt.Println("Could not retrieve theme details.")
				}
			}

			installedAt := "N/A"
			lastUpdated := "N/A"
			if globalEntry != nil { // globalEntry comes from GetThemeDetails
				if globalEntry.InstalledAt != "" {
					installedAt = globalEntry.InstalledAt
				}
				if globalEntry.LastUpdatedAt != "" {
					lastUpdated = globalEntry.LastUpdatedAt
				}
			} else if localMeta != nil {
				appLogger.Logf("Warning: Theme ID '%s' loaded locally but corresponding global state entry was not returned by GetThemeDetails.", themeID)
			}

			fmt.Printf("%-15s: %s\n", "INSTALLED_AT", installedAt)
			fmt.Printf("%-15s: %s\n", "LAST_UPDATED", lastUpdated)
		}
	},
}

func init() {
	themeCmd.AddCommand(themeInstallCmd) // Add install subcommand to theme command
	themeCmd.AddCommand(themeListCmd)    // Add list subcommand to theme command
	themeCmd.AddCommand(themeRemoveCmd)  // Add remove subcommand
	themeCmd.AddCommand(themeCreateCmd)  // Add create subcommand
	themeCmd.AddCommand(themeUpdateCmd)  // Add update subcommand

	// Add --force flag to theme install command
	themeInstallCmd.Flags().BoolP("force", "f", false, "Force overwrite if theme directory already exists")
	// Flags for themeCreateCmd are removed as it's now interactive.
}

var themeCreateCmd = &cobra.Command{
	Use:   "create <directory_path>",
	Short: "Create a theme.yaml by scanning an existing directory",
	Long: `Scans an existing directory to build a file structure, then prompts for theme metadata
(name, authors, version, description, source_link) and generates a 'theme.yaml' file
within that directory. The 'source_link' field is mandatory.`,
	Example: `  panelbase themes create ./my-existing-theme-files
  panelbase themes create /path/to/my-theme-project`,
	Args: cobra.ExactArgs(1), // Requires exactly one argument: the directory_path
	Run: func(cmd *cobra.Command, args []string) {
		dirPath := args[0]

		// Initialize base components needed for this command at the beginning
		cliLogger, _, idGen := initBaseForCLI()
		defer cliLogger.Close() // Ensure logger is closed when the command finishes

		// 1. Validate if dirPath is an existing directory
		fileInfo, err := os.Stat(dirPath)
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: Directory '%s' not found.\n", dirPath)
			// appLogger.Logf("Error for themes create: Directory '%s' not found.", dirPath) // Removed CLI layer log
			os.Exit(1)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accessing '%s': %v\n", dirPath, err)
			// appLogger.Logf("Error accessing '%s' for themes create: %v", dirPath, err) // Removed CLI layer log
			os.Exit(1)
		}
		if !fileInfo.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: '%s' is not a directory.\n", dirPath)
			// appLogger.Logf("Error for themes create: '%s' is not a directory.", dirPath) // Removed CLI layer log
			os.Exit(1)
		}

		absDirPath, err := filepath.Abs(dirPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting absolute path for '%s': %v\n", dirPath, err)
			// appLogger.Logf("Error getting absolute path for '%s': %v", dirPath, err) // Removed CLI layer log
			os.Exit(1)
		}

		// fmt.Printf("Scanning directory: %s\n", absDirPath) // Removed CLI layer output
		// fmt.Println("Please provide the following theme metadata:") // Removed CLI layer output

		// appLoggerForTM and idGen are now initialized at the top of the function as cliLogger and idGen.
		// defer appLoggerForTM.Close() // This defer is now handled by cliLogger.Close() above.

		reader := bufio.NewReader(os.Stdin)

		fmt.Print("Name: ")
		name, _ := reader.ReadString('\n')
		name = strings.TrimSpace(name)
		if name == "" {
			name = filepath.Base(absDirPath) // Default to directory name
		}

		fmt.Print("Authors: ")
		authorsStr, _ := reader.ReadString('\n')
		authorsStr = strings.TrimSpace(authorsStr)
		var authors []string
		if authorsStr == "" {
			authors = []string{"PanelBase Team"}
		} else {
			authors = strings.Split(authorsStr, ",")
			for i, a := range authors {
				authors[i] = strings.TrimSpace(a)
			}
		}

		fmt.Print("Version: ")
		version, _ := reader.ReadString('\n')
		version = strings.TrimSpace(version)
		if version == "" {
			version = "1.0.0"
		}

		fmt.Print("Description: ")
		description, _ := reader.ReadString('\n')
		description = strings.TrimSpace(description)
		if description == "" {
			description = "(Description? The author was too cool for that.)"
		}

		var sourceLinkInput string
		for {
			fmt.Print("Source Link: ")
			sourceLinkInput, _ = reader.ReadString('\n')
			sourceLinkInput = strings.TrimSpace(sourceLinkInput)
			if sourceLinkInput != "" {
				_, urlErr := url.ParseRequestURI(sourceLinkInput) // Basic URL validation
				if urlErr == nil {
					break
				}
				fmt.Println("Invalid URL format. Please enter a valid URL.")
			} else {
				fmt.Println("Source Link cannot be empty.")
			}
		}
		// No need to ensure trailing slash for source_link as it's a direct link or user-defined base.
		finalSourceLink := sourceLinkInput

		// Initialize ThemeManager
		themeMgr, err := themes.NewThemeManager(cliLogger, idGen) // Use cliLogger and idGen from the top
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Theme Manager: %v\n", err)
			// appLogger.Logf("Failed to initialize Theme Manager for create: %v", err) // Removed CLI layer log
			os.Exit(1)
		}

		// Call the new package-level Create function (which now only returns meta and error)
		createdMeta, err := themes.Create(themeMgr, absDirPath, name, authors, version, description, finalSourceLink)
		if err != nil {
			// Error message reflects the new purpose: creating the manifest file
			fmt.Fprintf(os.Stderr, "Error creating theme manifest in directory '%s': %v\n", absDirPath, err)
			os.Exit(1)
		}

		fmt.Printf("Theme manifest 'theme.yaml' created successfully in directory: %s\n\n", absDirPath)
		fmt.Printf("%-15s: %s\n", "Name", createdMeta.Name)
		var authorNames []string
		for _, author := range createdMeta.Authors {
			authorNames = append(authorNames, author.Name)
		}
		fmt.Printf("%-15s: %s\n", "Authors", strings.Join(authorNames, ", "))
		fmt.Printf("%-15s: %s\n", "Version", createdMeta.Version)
		fmt.Printf("%-15s: %s\n", "Description", createdMeta.Description)
		fmt.Printf("%-15s: %s\n", "Source Link", createdMeta.SourceLink)

		// Serialize and print Structure as YAML
		var structureYamlBuffer bytes.Buffer
		yamlEncoder := yaml.NewEncoder(&structureYamlBuffer)
		yamlEncoder.SetIndent(2) // Use 2-space indent for consistency

		// Encode the structure map directly
		if errEnc := yamlEncoder.Encode(createdMeta.Structure); errEnc != nil {
			fmt.Printf("%-15s: Error marshalling structure to YAML: %v\n", "Structure", errEnc)
		} else {
			// Trim trailing newline from buffer before printing for cleaner multi-line display
			structureYamlString := strings.TrimRight(structureYamlBuffer.String(), "\n")
			// Indent the YAML structure block for better readability under the "Structure:" label
			indentedStructure := ""
			if structureYamlString != "" { // Avoid indenting an empty string
				lines := strings.Split(structureYamlString, "\n")
				for i, line := range lines {
					if i == 0 { // First line is already under "Structure:"
						indentedStructure += line
					} else {
						indentedStructure += "\n  " + line // Indent subsequent lines
					}
				}
			}
			fmt.Printf("%-15s:\n%s\n", "Structure", indentedStructure)
		}

		fmt.Println("\nNote: The theme manifest has been created. To install this theme, you can use:")
		// Construct the relative path if absDirPath is within current working dir, otherwise use abs path.
		// For simplicity here, we'll use the absolute path.
		manifestPathForInstall := filepath.Join(absDirPath, "theme.yaml")
		fmt.Printf("  panelbase themes install \"%s\"\n", manifestPathForInstall)

	},
}

var themeUpdateCmd = &cobra.Command{
	Use:   "update <theme_id>",
	Short: "Update an installed theme to the latest version from its source",
	Long: `Checks the theme's original source link for a newer version and updates the theme if available.
The theme_id is the local directory name of the theme (e.g., "thm_abc123").`,
	Example: `  panelbase themes update thm_J4yoW1B5kDzy`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		themeID := args[0]
		appLogger, _, idGen := initBaseForCLI()
		defer appLogger.Close() // Ensure logger is closed

		themeMgr, err := themes.NewThemeManager(appLogger, idGen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Theme Manager: %v\n", err)
			// appLogger.Logf("Failed to initialize Theme Manager for update: %v", err) // Removed CLI layer log
			os.Exit(1)
		}

		// Call the new package-level Update function
		_, err = themes.Update(themeMgr, themeID) // updatedMeta is no longer needed here
		if err != nil {
			// themes.Update method in manager should log details, including "no update needed" scenarios.
			fmt.Fprintf(os.Stderr, "Error updating theme '%s': %v\n", themeID, err)
			os.Exit(1)
		}
		// Success or "no update needed" messages are now handled by the themes package logging.
	},
}

var themeRemoveCmd = &cobra.Command{
	Use:   "remove <theme_id>",
	Short: "Remove an installed theme",
	Long: `Removes a theme based on its ID. This action will:
1. Remove the theme's entry from the global state file (configs/themes.json).
2. Delete the theme's directory from 'ext/themes/'.`,
	Example: `  panelbase themes remove thm_J4yoW1B5kDzy`,
	Args:    cobra.ExactArgs(1), // Requires exactly one argument: the theme_id
	Run: func(cmd *cobra.Command, args []string) {
		themeID := args[0]

		appLogger, _, idGen := initBaseForCLI() // Initialize base components

		themeMgr, err := themes.NewThemeManager(appLogger, idGen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Theme Manager: %v\n", err)
			os.Exit(1)
		}

		// Call the new package-level Remove function
		if err := themes.Remove(themeMgr, themeID); err != nil {
			// The themes.Remove function in the manager already logs details.
			// We just need to inform the user via stderr if an error occurs.
			fmt.Fprintf(os.Stderr, "Error removing theme '%s': %v\n", themeID, err)
			os.Exit(1)
		}
		// Success message is now handled by the themes package logging.
	},
}

// --- Plugin Command ---
var pluginCmd = &cobra.Command{
	Use:   "plugins", // Changed from "plugin" to "plugins"
	Short: "Manage plugins",
	Long:  `Commands for installing, listing, updating, and removing plugins.`,
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a plugin from a URL or local path",
	Long: `Downloads and installs a plugin from the specified source.
The source must be either a direct URL pointing to a 'plugin.yaml' file
or a local filesystem path to a 'plugin.yaml' file.`,
	Example: `  panelbase plugin install https://example.com/path/to/myplugin.yaml
  panelbase plugin install /path/to/local/plugin.yaml --force`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		force, _ := cmd.Flags().GetBool("force")

		// Initialize dependencies (Logger, Config, IDGen)
		appLogger, _, idGen := initBaseForCLI() // Use helper, ignore cfg for now

		// Initialize Plugin Manager
		// Plugin Manager initialization requires RPC address, handled in startPanelBaseServer
		pluginMgr, err := plugins.NewPluginManager(appLogger, idGen)
		if err != nil {
			appLogger.Logf("Failed to initialize Plugin Manager for install: %v", err)
			fmt.Fprintf(os.Stderr, "Failed to initialize Plugin Manager: %v\n", err)
			os.Exit(1)
		}

		// Call the install method
		_, err = pluginMgr.InstallPlugin(source, force)
		if err != nil {
			appLogger.Logf("Error installing plugin: %v", err)
			fmt.Fprintf(os.Stderr, "Error installing plugin: %v\n", err)
			os.Exit(1)
		} // Closing brace for if err != nil
	}, // Closing brace for pluginInstallCmd.Run
} // Closing brace for pluginInstallCmd variable definition
var pluginListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List installed plugins",
	Long:    `Displays a list of all plugins currently installed and registered in the state file.`,
	Example: `  panelbase plugin list`,
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		appLogger, _, idGen := initBaseForCLI() // Use helper, appLogger still needed for manager init

		// Initialize Plugin Manager
		// Note: NewPluginManager might require rpcAddrForPlugins if it needs to communicate.
		// For a simple list command that reads from a state file, it might not be strictly necessary,
		// but the current NewPluginManager signature in the full file includes it.
		// Assuming for CLI list, we might need a simplified NewPluginManager or pass a dummy/default.
		// For now, let's assume NewPluginManager can handle a potentially empty rpcAddr for CLI list.
		// This was simplified in a previous step for the CLI context.
		pluginMgr, err := plugins.NewPluginManager(appLogger, idGen) // Simplified for CLI
		if err != nil {
			// appLogger.Logf("Failed to initialize Plugin Manager for list: %v", err) // No internal logging for CLI list
			fmt.Fprintf(os.Stderr, "Failed to initialize Plugin Manager: %v\n", err)
			os.Exit(1)
		}

		// Call the list method
		installedPlugins, err := pluginMgr.ListInstalledPlugins()
		if err != nil {
			// appLogger.Logf("Error listing plugins: %v", err) // No internal logging
			fmt.Fprintf(os.Stderr, "Error listing plugins: %v\n", err)
			os.Exit(1)
		}

		if len(installedPlugins) == 0 {
			fmt.Println("No plugins installed.")
			return
		}

		headers := []string{"ID", "NAME", "VERSION"}
		columnWidths := make([]int, len(headers)) // Correctly initialize columnWidths

		// Set initial widths based on headers, NAME column fixed to 15
		columnWidths[0] = calculateDisplayWidth(headers[0]) // ID
		columnWidths[1] = 15                                // NAME (fixed)
		columnWidths[2] = calculateDisplayWidth(headers[2]) // VERSION

		if headerNameWidth := calculateDisplayWidth(headers[1]); headerNameWidth > columnWidths[1] {
			columnWidths[1] = headerNameWidth
		}

		type pluginRow struct {
			ID      string
			Name    string // This will be the truncated name
			Version string
		}
		var rows []pluginRow

		for _, p := range installedPlugins {
			displayName := truncateStringToDisplayWidth(p.Name, 15)
			row := pluginRow{p.PlgID, displayName, p.Version}
			rows = append(rows, row)

			if idWidth := calculateDisplayWidth(row.ID); idWidth > columnWidths[0] {
				columnWidths[0] = idWidth
			}
			// NAME column width is fixed
			if versionWidth := calculateDisplayWidth(row.Version); versionWidth > columnWidths[2] {
				columnWidths[2] = versionWidth
			}
		}

		// Print header
		for i, h := range headers {
			fmt.Print(h)
			padding := columnWidths[i] - calculateDisplayWidth(h)
			if padding > 0 {
				fmt.Print(strings.Repeat(" ", padding))
			}
			if i < len(headers)-1 {
				fmt.Print("  ") // Two spaces as separator
			}
		}
		fmt.Println()

		// Print data rows
		for _, row := range rows {
			cells := []string{row.ID, row.Name, row.Version}
			for i, cell := range cells {
				fmt.Print(cell)
				padding := columnWidths[i] - calculateDisplayWidth(cell)
				if padding > 0 {
					fmt.Print(strings.Repeat(" ", padding))
				}
				if i < len(cells)-1 {
					fmt.Print("  ") // Two spaces as separator
				}
			}
			fmt.Println()
		}
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <plugin_id>",
	Short: "Remove (uninstall) an installed plugin",
	Long: `Removes the plugin specified by its unique ID (directory name, e.g., 'plg_xxxxx').
This involves deleting the plugin's directory and updating the state file.`,
	Example: `  panelbase plugin remove plg_abc123`,
	Args:    cobra.ExactArgs(1), // Requires exactly one argument: the plugin ID
	Run: func(cmd *cobra.Command, args []string) {
		pluginID := args[0]

		appLogger, _, idGen := initBaseForCLI() // Use helper

		// Initialize Plugin Manager
		pluginMgr, err := plugins.NewPluginManager(appLogger, idGen)
		if err != nil {
			appLogger.Logf("Failed to initialize Plugin Manager for remove: %v", err)
			fmt.Fprintf(os.Stderr, "Failed to initialize Plugin Manager: %v\n", err)
			os.Exit(1)
		}

		// Call the remove method
		err = pluginMgr.RemovePlugin(pluginID)
		if err != nil {
			appLogger.Logf("Error removing plugin %s: %v", pluginID, err)
			fmt.Fprintf(os.Stderr, "Error removing plugin %s: %v\n", pluginID, err)
			os.Exit(1)
		}
		fmt.Printf("Plugin %s removed successfully.\n", pluginID)
	},
}

var pluginUpdateCmd = &cobra.Command{
	Use:   "update <plugin_id>",
	Short: "Update an installed plugin",
	Long: `Updates an installed plugin by re-fetching its metadata and files from its original source.
This command effectively performs a remove followed by an install, preserving the plugin ID.
If the plugin's source link is not available or the update process fails, the original plugin installation might be affected.`,
	Example: `  panelbase plugin update plg_abc123`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pluginID := args[0]
		appLogger, _, idGen := initBaseForCLI()

		pluginMgr, err := plugins.NewPluginManager(appLogger, idGen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Plugin Manager: %v\n", err)
			os.Exit(1)
		}

		_, err = pluginMgr.UpdatePlugin(pluginID) // Correctly assign two return values
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating plugin %s: %v\n", pluginID, err)
			os.Exit(1)
		}
		fmt.Printf("Plugin %s updated successfully.\n", pluginID)
	},
}

func init() {
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	pluginCmd.AddCommand(pluginUpdateCmd)
	pluginInstallCmd.Flags().BoolP("force", "f", false, "Force overwrite if plugin directory already exists")
}

// --- Custom Command ---
var commandCmd = &cobra.Command{
	Use:   "commands", // Changed from "command" to "commands"
	Short: "Manage custom commands",
	Long:  `Commands for installing, listing, and managing custom command scripts.`,
}

var commandInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a custom command from a URL or local path",
	Long: `Downloads and installs a custom command script from the specified source.
The source must be either a direct URL pointing to a 'command.yaml' metadata file
or a local filesystem path to a 'command.yaml' file. The associated script will also be downloaded.`,
	Example: `  panelbase command install https://example.com/path/to/mycommand.yaml
  panelbase command install /path/to/local/mycommand.yaml --force`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]
		force, _ := cmd.Flags().GetBool("force")

		appLogger, _, idGen := initBaseForCLI()

		commandMgr, err := commands.NewCommandManager(appLogger, idGen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Command Manager: %v\n", err)
			os.Exit(1)
		}

		_, err = commandMgr.InstallCommand(source, force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error installing command: %v\n", err)
			os.Exit(1)
		}
		// Success message logged internally by InstallCommand
	},
} // Closing brace for commandInstallCmd variable definition
var commandListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List installed custom commands",
	Long:    `Displays a list of all custom command scripts currently installed and registered.`,
	Example: `  panelbase command list`,
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		appLogger, _, idGen := initBaseForCLI() // Use helper, appLogger for manager init

		// Initialize Command Manager
		commandMgr, err := commands.NewCommandManager(appLogger, idGen)
		if err != nil {
			// appLogger.Logf("Failed to initialize Command Manager for list: %v", err) // No internal logging
			fmt.Fprintf(os.Stderr, "Failed to initialize Command Manager: %v\n", err)
			os.Exit(1)
		}

		// Call the list method
		installedCommands := commandMgr.ListInstalledCommands() // Assumes this returns []*CommandMetadata or similar

		if len(installedCommands) == 0 {
			fmt.Println("No custom commands installed.")
			return
		}

		headers := []string{"ID (FILEPATH)", "NAME", "VERSION", "DESCRIPTION"}
		columnWidths := make([]int, len(headers)) // Correctly initialize columnWidths

		// Set initial widths based on headers, NAME column fixed to 15
		columnWidths[0] = calculateDisplayWidth(headers[0]) // ID (FILEPATH)
		columnWidths[1] = 15                                // NAME (fixed)
		columnWidths[2] = calculateDisplayWidth(headers[2]) // VERSION
		columnWidths[3] = calculateDisplayWidth(headers[3]) // DESCRIPTION

		if headerNameWidth := calculateDisplayWidth(headers[1]); headerNameWidth > columnWidths[1] {
			columnWidths[1] = headerNameWidth
		}

		type commandRow struct {
			FilePath    string
			CommandName string // This will be the truncated name
			Version     string
			Description string
		}
		var rows []commandRow

		for _, c := range installedCommands {
			desc := c.Description
			if desc == "" {
				desc = "N/A"
			}
			displayName := truncateStringToDisplayWidth(c.Command, 15)
			row := commandRow{c.FilePath, displayName, c.Version, desc}
			rows = append(rows, row)

			if filePathWidth := calculateDisplayWidth(row.FilePath); filePathWidth > columnWidths[0] {
				columnWidths[0] = filePathWidth
			}
			// NAME column width is fixed
			if nameContentWidth := calculateDisplayWidth(row.CommandName); nameContentWidth > columnWidths[1] {
				// This ensures the column is wide enough for the content, even if header is shorter
				// but it should not exceed the fixed width of 15 for the content itself.
				// The columnWidths[1] is already set to 15 (or header width if > 15).
				// We need to ensure the column can hold the longest truncated name.
				if nameContentWidth > columnWidths[1] { // This logic might be redundant if displayName is already truncated to 15
					columnWidths[1] = nameContentWidth
				}
			}
			if versionWidth := calculateDisplayWidth(row.Version); versionWidth > columnWidths[2] {
				columnWidths[2] = versionWidth
			}
			// For Description, we might want to truncate it as well if it's too long,
			// or let it be and potentially wrap if the terminal supports it.
			// For now, let's calculate its dynamic width.
			if descriptionWidth := calculateDisplayWidth(row.Description); descriptionWidth > columnWidths[3] {
				columnWidths[3] = descriptionWidth
			}
		}

		// Print header
		for i, h := range headers {
			fmt.Print(h)
			padding := columnWidths[i] - calculateDisplayWidth(h)
			if padding > 0 {
				fmt.Print(strings.Repeat(" ", padding))
			}
			if i < len(headers)-1 {
				fmt.Print("  ") // Two spaces as separator
			}
		}
		fmt.Println()

		// Print data rows
		for _, row := range rows {
			cells := []string{row.FilePath, row.CommandName, row.Version, row.Description}
			for i, cell := range cells {
				fmt.Print(cell)
				padding := columnWidths[i] - calculateDisplayWidth(cell)
				if padding > 0 {
					fmt.Print(strings.Repeat(" ", padding))
				}
				if i < len(cells)-1 {
					fmt.Print("  ") // Two spaces as separator
				}
			}
			fmt.Println()
		}
	},
} // Closing brace for commandListCmd variable definition
var commandRemoveCmd = &cobra.Command{
	Use:   "remove <command_id>",
	Short: "Remove (uninstall) an installed command script",
	Long: `Removes the command script specified by its unique ID (filename, e.g., 'cmd_xxxxx.sh').
This involves deleting the script file and updating the state file.`,
	Example: `  panelbase command remove cmd_abc123.sh`,
	Args:    cobra.ExactArgs(1), // Requires exactly one argument: the command ID
	Run: func(cmd *cobra.Command, args []string) {
		commandID := args[0]

		appLogger, _, idGen := initBaseForCLI() // Use helper

		// Initialize Command Manager
		commandMgr, err := commands.NewCommandManager(appLogger, idGen)
		if err != nil {
			appLogger.Logf("Failed to initialize Command Manager for remove: %v", err)
			fmt.Fprintf(os.Stderr, "Failed to initialize Command Manager: %v\n", err)
			os.Exit(1)
		}

		// Call the remove method
		err = commandMgr.RemoveCommand(commandID)
		if err != nil {
			appLogger.Logf("Error removing command %s: %v", commandID, err)
			fmt.Fprintf(os.Stderr, "Error removing command %s: %v\n", commandID, err)
			os.Exit(1)
		}
		fmt.Printf("Command %s removed successfully.\n", commandID)
	},
}

var commandUpdateCmd = &cobra.Command{
	Use:   "update <command_id>",
	Short: "Update an installed command script",
	Long: `Updates an installed command script by re-fetching its metadata and files from its original source.
This command effectively performs a remove followed by an install, preserving the command ID.
If the command's source link is not available or the update process fails, the original command installation might be affected.`,
	Example: `  panelbase command update cmd_abc123.sh`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		commandID := args[0]
		appLogger, _, idGen := initBaseForCLI()

		commandMgr, err := commands.NewCommandManager(appLogger, idGen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize Command Manager: %v\n", err)
			os.Exit(1)
		}

		_, err = commandMgr.UpdateCommand(commandID) // Correctly assign two return values
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating command %s: %v\n", commandID, err)
			os.Exit(1)
		}
		fmt.Printf("Command %s updated successfully.\n", commandID)
	},
}

func init() {
	commandCmd.AddCommand(commandInstallCmd)
	commandCmd.AddCommand(commandListCmd)
	commandCmd.AddCommand(commandRemoveCmd)
	commandCmd.AddCommand(commandUpdateCmd)
	commandInstallCmd.Flags().BoolP("force", "f", false, "Force overwrite if command script already exists")
}

// --- Container Command ---
var containerCmd = &cobra.Command{
	Use:   "containers", // Changed from "container" to "containers"
	Short: "Manage containers",
	Long:  `Commands for starting, stopping, and listing application containers.`,
}

var containerStartCmd = &cobra.Command{
	Use:   "start <container_id>",
	Short: "Start a web server for a given container ID",
	Long: `Starts a web server for the specified container ID.
The container must have a valid 'webconfiguration.yaml' in its directory.
PanelBase will manage the server process.`,
	Example: `  panelbase container start my_container_id`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		containerID := args[0]
		appLogger, containerMgr := initForContainerCLI()

		appLogger.Logf("Attempting to start web server for container: %s", containerID)
		err := containerMgr.StartWebServer(containerID)
		if err != nil {
			appLogger.Logf("Error starting container %s: %v", containerID, err)
			fmt.Fprintf(os.Stderr, "Error starting container %s: %v\n", containerID, err)
			os.Exit(1)
		}
		// Note: StartWebServer logs success internally.
		fmt.Printf("Successfully started container %s.\n", containerID)
	},
}

var containerStopCmd = &cobra.Command{
	Use:     "stop <container_id>",
	Short:   "Stop the web server for a given container ID",
	Long:    `Stops the web server associated with the specified container ID.`,
	Example: `  panelbase container stop my_container_id`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		containerID := args[0]
		appLogger, containerMgr := initForContainerCLI()

		appLogger.Logf("Attempting to stop web server for container: %s", containerID)
		err := containerMgr.StopWebServer(containerID)
		if err != nil {
			appLogger.Logf("Error stopping container %s: %v", containerID, err)
			fmt.Fprintf(os.Stderr, "Error stopping container %s: %v\n", containerID, err)
			os.Exit(1)
		}
		// Note: StopWebServer logs success internally.
		fmt.Printf("Successfully stopped container %s.\n", containerID)
	},
}

var containerListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all managed containers and their status",
	Long:    `Displays a list of all containers discovered by PanelBase, along with their current status, port, and web directory.`,
	Example: `  panelbase container list`,
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		_, containerMgr := initForContainerCLI() // appLogger no longer needed for direct logging here

		// appLogger.Log("Listing containers...") // Removed internal log
		ids := containerMgr.ListContainers()
		if len(ids) == 0 {
			fmt.Println("No containers found.")
			return
		}

		headers := []string{"ID", "NAME (WEBDIR)", "STATUS", "PORT", "LAST ERROR"}
		columnWidths := make([]int, len(headers)) // Correctly initialize columnWidths

		// Set initial widths based on headers, NAME (WEBDIR) column fixed to 15
		columnWidths[0] = calculateDisplayWidth(headers[0]) // ID
		columnWidths[1] = 15                                // NAME (WEBDIR) (fixed)
		columnWidths[2] = calculateDisplayWidth(headers[2]) // STATUS
		columnWidths[3] = calculateDisplayWidth(headers[3]) // PORT
		columnWidths[4] = calculateDisplayWidth(headers[4]) // LAST ERROR

		if headerNameWidth := calculateDisplayWidth(headers[1]); headerNameWidth > columnWidths[1] {
			columnWidths[1] = headerNameWidth
		}

		type containerRow struct {
			ID        string
			WebDir    string // This will be the truncated name
			Status    string
			Port      string
			LastError string
		}
		var rows []containerRow

		for _, id := range ids {
			info, exists := containerMgr.GetContainerInfo(id)
			var row containerRow
			if exists {
				lastErrStr := info.LastError
				if lastErrStr == "" {
					lastErrStr = "N/A"
				}
				displayWebDir := truncateStringToDisplayWidth(info.WebDir, 15)
				row = containerRow{info.ID, displayWebDir, string(info.Status), fmt.Sprintf("%d", info.Port), lastErrStr}
			} else {
				fmt.Fprintf(os.Stderr, "Error: Container ID %s listed but details not found.\n", id)
				// For error case, WebDir is N/A, which is short, no truncation needed.
				row = containerRow{id, "N/A", "Error", "N/A", "Details not found"}
			}
			rows = append(rows, row)

			if idWidth := calculateDisplayWidth(row.ID); idWidth > columnWidths[0] {
				columnWidths[0] = idWidth
			}
			// NAME (WEBDIR) column width is fixed
			if nameContentWidth := calculateDisplayWidth(row.WebDir); nameContentWidth > columnWidths[1] {
				columnWidths[1] = nameContentWidth
			}
			if statusWidth := calculateDisplayWidth(row.Status); statusWidth > columnWidths[2] {
				columnWidths[2] = statusWidth
			}
			if portWidth := calculateDisplayWidth(row.Port); portWidth > columnWidths[3] {
				columnWidths[3] = portWidth
			}
			if lastErrorWidth := calculateDisplayWidth(row.LastError); lastErrorWidth > columnWidths[4] {
				columnWidths[4] = lastErrorWidth
			}
		}

		// Print header
		for i, h := range headers {
			fmt.Print(h)
			padding := columnWidths[i] - calculateDisplayWidth(h)
			if padding > 0 {
				fmt.Print(strings.Repeat(" ", padding))
			}
			if i < len(headers)-1 {
				fmt.Print("  ") // Two spaces as separator
			}
		}
		fmt.Println()

		// Print data rows
		for _, row := range rows {
			cells := []string{row.ID, row.WebDir, row.Status, row.Port, row.LastError}
			for i, cell := range cells {
				fmt.Print(cell)
				padding := columnWidths[i] - calculateDisplayWidth(cell)
				if padding > 0 {
					fmt.Print(strings.Repeat(" ", padding))
				}
				if i < len(cells)-1 {
					fmt.Print("  ") // Two spaces as separator
				}
			}
			fmt.Println()
		}
	}, // Closing brace for Run func
} // Closing brace for containerListCmd
func init() {
	// Add subcommands to containerCmd
	containerCmd.AddCommand(containerStartCmd)
	containerCmd.AddCommand(containerStopCmd)
	containerCmd.AddCommand(containerListCmd)
	// Add flags to container commands if needed later (e.g., --port for create)
}

// initForContainerCLI initializes Logger, Config, IDGenerator, and ContainerManager
// needed for container CLI commands. Exits on fatal initialization error.
func initForContainerCLI() (*logger.Logger, *container.ContainerManager) {
	// For CLI, initialize logger but rely on fmt for direct user output.
	appLogger, err := logger.NewLogger() // Use standard initialization
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger for CLI: %v\n", err)
		os.Exit(1)
	}
	// No defer close here, as the command finishes quickly.

	cfg, err := configuration.LoadConfig()
	if err != nil {
		appLogger.Logf("Failed to load configuration for container CLI: %v", err)
		if cfg == nil { // Ensure cfg is checked for nil before accessing its members
			fmt.Fprintf(os.Stderr, "Critical: Configuration object is nil after LoadConfig failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	idGen, err := utils.NewIDGenerator(&cfg.Security)
	if err != nil {
		appLogger.Logf("Failed to initialize ID generator for container CLI: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to initialize ID generator: %v\n", err)
		os.Exit(1)
	}

	// Corrected NewContainerManager call
	containerMgr, err := container.NewContainerManager(idGen, cfg.Server.Host, appLogger)
	if err != nil {
		appLogger.Logf("Failed to initialize Container Manager for CLI: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to initialize Container Manager: %v\n", err)
		os.Exit(1)
	}
	return appLogger, containerMgr
}

// initBaseForCLI initializes Logger, Config, and IDGenerator.
// It's a common utility for CLI commands that don't need the full server setup
// but require these base components. Exits on fatal initialization error.
func initBaseForCLI() (*logger.Logger, *configuration.Config, *utils.IDGenerator) {
	appLogger, err := logger.NewLogger()
	if err != nil {
		log.Fatalf("Failed to initialize logger for CLI: %v", err)
	}

	cfg, err := configuration.LoadConfig()
	if err != nil {
		appLogger.Logf("Failed to load configuration for CLI: %v", err)
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "Critical: Configuration object is nil after LoadConfig failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	idGen, err := utils.NewIDGenerator(&cfg.Security)
	if err != nil {
		appLogger.Logf("Failed to initialize ID generator for CLI: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to initialize ID generator: %v\n", err)
		os.Exit(1)
	}

	return appLogger, cfg, idGen
}

// startPanelBaseServer initializes and starts all core components of PanelBase.
func startPanelBaseServer(cmd *cobra.Command, args []string) {
	// Initialize Logger
	appLogger, err := logger.NewLogger()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer appLogger.Close()
	appLogger.Log("Logger initialized.")

	// Load Configuration
	appConfig, err := configuration.LoadConfig()
	if err != nil {
		appLogger.Logf("Failed to load configuration: %v", err)
		os.Exit(1)
	}
	appLogger.Log("Configuration loaded.")
	// Removed logging of appConfig.Paths as they are now hardcoded in managers

	// Initialize ID Generator
	idGenerator, err := utils.NewIDGenerator(&appConfig.Security)
	if err != nil {
		appLogger.Logf("Failed to initialize ID generator: %v", err)
		os.Exit(1)
	}
	appLogger.Log("ID Generator initialized.")

	// Initialize Managers
	// Corrected NewContainerManager call
	containerMgr, err := container.NewContainerManager(idGenerator, appConfig.Server.Host, appLogger)
	if err != nil {
		appLogger.Logf("Failed to initialize Container Manager: %v", err)
		os.Exit(1)
	}
	appLogger.Log("Container Manager initialized.")

	themeMgr, err := themes.NewThemeManager(appLogger, idGenerator) // Removed path argument
	if err != nil {
		appLogger.Logf("Failed to initialize Theme Manager: %v", err)
		os.Exit(1)
	}
	appLogger.Log("Theme Manager initialized.")

	pluginMgr, err := plugins.NewPluginManager(appLogger, idGenerator) // Removed path argument
	if err != nil {
		appLogger.Logf("Failed to initialize Plugin Manager: %v", err)
		os.Exit(1)
	}
	appLogger.Log("Plugin Manager initialized.")

	commandMgr, err := commands.NewCommandManager(appLogger, idGenerator) // Removed path argument
	if err != nil {
		appLogger.Logf("Failed to initialize Command Manager: %v", err)
		os.Exit(1)
	}
	appLogger.Log("Command Manager initialized.")

	// Suppress "declared and not used" errors for managers if RPC server doesn't use them yet.
	// This is a temporary measure. Ideally, RPC server would use these managers.
	_ = containerMgr
	_ = themeMgr
	_ = pluginMgr
	_ = commandMgr

	// Start RPC Server
	rpcHost := appConfig.Server.Host
	rpcPort := appConfig.Server.Port

	rpcReadyChan := make(chan struct{})
	err = rpc.StartRPCServer(appLogger, idGenerator, rpcHost, rpcPort, rpcReadyChan)
	if err != nil {
		appLogger.Logf("Failed to start RPC server: %v", err)
		os.Exit(1)
	}

	<-rpcReadyChan
	appLogger.Logf("RPC server started and listening on %s:%d.", rpcHost, rpcPort)

	appLogger.Log("PanelBase server is running. Press Ctrl+C to stop.")
	select {}
}
