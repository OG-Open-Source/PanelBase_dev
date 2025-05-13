# Theme Management Module Refactor Plan

## 1. Introduction and Goal

This document outlines the plan to refactor the theme management functionality within the `internal/extension/themes` package of PanelBase. The primary goals are:
- To provide a clear, package-level API for theme operations (Install, Update, Remove, List, Create).
- To improve code modularity, readability, and maintainability by extracting shared logic into internal helper methods.
- To reduce code duplication across different theme management functions.
- To formally integrate a `Create` theme functionality into the `themes` package API.

## 2. Core Design Changes

-   **Package-Level API**: The `themes` package will export a set of functions for theme operations (e.g., `themes.Install()`, `themes.Update()`).
-   **`ThemeManager` Role**:
    -   The `ThemeManager` struct and its constructor `NewThemeManager` will be retained. It will manage theme-related configurations (logger, ID generator, paths), internal state (e.g., cache of installed themes), and the underlying logic for operations.
    -   The new package-level API functions will accept a `*ThemeManager` instance as a parameter, delegating the actual work to the `ThemeManager`'s methods (which will mostly become private/internal).
-   **Internal Helper Methods**: Shared logic (e.g., definition fetching/validation, directory management, asset downloading, state updates) will be extracted into private helper methods of the `ThemeManager`.

## 3. Proposed Package-Level API Function Signatures

Located in the `internal/extension/themes` package:

```go
// Install downloads and installs a theme from a given source.
func Install(tm *ThemeManager, source string, force bool) (*ThemeMetadata, error)

// Update checks for a newer version of an installed theme and updates it.
func Update(tm *ThemeManager, themeID string) (*ThemeMetadata, error)

// Remove uninstalls a theme.
func Remove(tm *ThemeManager, themeID string) error

// List retrieves a map of installed themes, keyed by their ID.
// Returns InstalledThemeEntry which includes ID, name, version, and timestamps.
func List(tm *ThemeManager) (map[string]config.InstalledThemeEntry, error)

// Create registers a new theme from a given directory path and provided metadata.
// It scans the directory for its structure, creates theme.json, copies files if necessary,
// and updates the global themes state.
func Create(tm *ThemeManager, directoryPath string, name string, authors []string, version string, description string, sourceLink string) (*ThemeMetadata, error)
```

## 4. Key Internal Helper Methods (Private methods of `ThemeManager`)

This is a non-exhaustive list of helper methods to be created/refined:

-   `_fetchAndValidateDefinition(source string) (*ThemeMetadata, sourceDetails, error)`: Fetches, parses, and validates `theme.yaml` from a source.
-   `_determineInstallAction(meta *ThemeMetadata, force bool) (actionType ActionType, existingID string, err error)`: Determines if an install is new, an overwrite, or an error due to existence.
-   `_prepareThemeDirectory(themeID string, meta *ThemeMetadata, actionType ActionType) (themePath string, err error)`: Manages the creation/cleanup of a theme's directory, including security checks.
-   `_downloadThemeAssets(themePath string, structure map[string]interface{}, baseURL *url.URL) error`: Downloads all assets defined in the theme's structure.
-   `_writeLocalThemeJSON(themePath string, meta *ThemeMetadata) error`: Creates or updates the `theme.json` file within a specific theme's directory.
-   `_updateGlobalThemesState(themeID string, entry config.InstalledThemeEntry, operation string) error`: Atomically loads, modifies, and saves the global `configs/themes.json` state file. `operation` can be "add", "update", or "remove".
-   `_scanDirectoryForStructure(dirPath string) (map[string]interface{}, error)`: (For `Create`) Scans a directory to generate the `structure` map for `ThemeMetadata`.
-   `_copyThemeFilesToManagedDir(sourceDirPath string, targetThemePath string) error`: (For `Create`) Copies files from a user-provided directory to the PanelBase-managed theme directory.
-   `_performPathSecurityChecks(targetDirName string) (absTargetThemePath string, err error)`: Ensures path operations are within the allowed `themeDir`.

## 5. Refactoring Steps Overview

1.  **Define Package-Level Functions**: Create the skeletons for the new public API functions (`Install`, `Update`, `Remove`, `List`, `Create`) in `manager.go` or a new `api.go`.
2.  **Implement Helper Methods**: Incrementally implement the private helper methods by extracting and refactoring logic from the current `ThemeManager.InstallTheme`, `ThemeManager.UpdateTheme`, and `ThemeManager.RemoveTheme` methods.
3.  **Refactor API Functions**: Modify the package-level API functions to orchestrate calls to these helper methods.
4.  **Implement `Create` Function**: Implement the new `themes.Create()` function and its specific helper methods (e.g., `_scanDirectoryForStructure`, `_copyThemeFilesToManagedDir`).
5.  **Adjust `ThemeManager`**: Remove or deprecate old public methods from `ThemeManager` if they are fully replaced by the new package-level API.
6.  **Update CLI Commands**: Modify `cmd/server/main.go` so that `theme*Cmd` commands initialize a `ThemeManager` and then call the new package-level API functions from the `themes` package.
7.  **Testing**: Conduct thorough unit and integration tests for all refactored and new functionalities.
8.  **Documentation**: Update `docs/theme_command_logging_design.md` to include logging for the `Create` command and verify accuracy for other commands post-refactor.

## 6. `Create` Function Detailed Flow

The `themes.Create()` function will:
1.  Log the attempt: `Attempting to create theme '[NAME]' from directory '[DIR_PATH]'...`
2.  Scan the `directoryPath` using `_scanDirectoryForStructure` to generate the `structure` map.
3.  Combine user-provided metadata (name, authors, etc.) with the scanned `structure` into a `ThemeMetadata` object.
4.  Validate this `ThemeMetadata`.
5.  Generate a new unique theme ID (`newThemeID`) using `tm.idGen`.
6.  Create the target theme directory: `ext/themes/[newThemeID]`.
7.  Copy contents from `directoryPath` to `ext/themes/[newThemeID]` using `_copyThemeFilesToManagedDir`.
8.  Create `theme.json` in `ext/themes/[newThemeID]` using `_writeLocalThemeJSON`.
9.  Update `configs/themes.json` using `_updateGlobalThemesState` with an "add" operation.
10. Refresh `ThemeManager`'s internal cache by calling `tm.discoverThemes()`.
11. Log success: `Theme '[NAME]' (ID: [newThemeID]) created successfully from directory '[DIR_PATH]' and registered.`
12. Return the created `*ThemeMetadata` or an error.

## 7. Mermaid Diagram (High-Level API Call Flow)

```mermaid
graph LR
    subgraph CLI Layer (cmd/server/main.go)
        TCmd[theme*Cmd Commands]
    end

    subgraph Themes Package API (internal/extension/themes)
        direction LR
        API_Install["themes.Install(tm, ...)"]
        API_Update["themes.Update(tm, ...)"]
        API_Remove["themes.Remove(tm, ...)"]
        API_List["themes.List(tm, ...)"]
        API_Create["themes.Create(tm, ...)"]
    end

    subgraph ThemeManager (Internal Logic & State)
        TM[ThemeManager Instance]
        subgraph Helper Methods (Private)
            direction TB
            HM_Fetch["_fetchAndValidateDefinition"]
            HM_Dir["_manageThemeDirectory"]
            HM_Download["_downloadThemeAssets"]
            HM_LocalJSON["_updateLocalManifest"]
            HM_GlobalState["_updateGlobalState"]
            HM_Scan["_scanDirectoryForStructure"]
            HM_Copy["_copyThemeFilesToManagedDir"]
            HM_Etc["... (other helpers)"]
        end
    end

    GlobalStateFile["configs/themes.json"]
    ThemeFileSystem["ext/themes/theme_id/*"]

    TCmd -- Creates --> TM
    TCmd -- Calls --> API_Install
    TCmd -- Calls --> API_Update
    TCmd -- Calls --> API_Remove
    TCmd -- Calls --> API_List
    TCmd -- Calls --> API_Create

    API_Install -- Passes & Calls Methods --> TM
    API_Update -- Passes & Calls Methods --> TM
    API_Remove -- Passes & Calls Methods --> TM
    API_List -- Passes & Calls Methods --> TM
    API_Create -- Passes & Calls Methods --> TM

    TM -- Uses --> Helper Methods
    Helper Methods -- Interact With --> GlobalStateFile
    Helper Methods -- Interact With --> ThemeFileSystem
```

## 8. Expected Benefits

-   **Improved Modularity**: Clear separation between the public API and internal implementation details.
-   **Reduced Duplication**: Common tasks are handled by shared helper methods.
-   **Enhanced Readability**: Public API functions and `ThemeManager` methods become shorter and more focused.
-   **Better Testability**: Smaller, focused helper methods are easier to unit test.
-   **Consistent API**: All theme operations are exposed uniformly at the package level.

## 9. Potential Risks & Considerations

-   **Refactoring Complexity**: This is a significant refactor and requires careful attention to detail to avoid breaking existing functionality.
-   **Thorough Testing**: Extensive testing will be crucial to ensure all scenarios are covered.
-   **API Design Details**: The exact signatures and error handling davranış of helper methods will need careful design during implementation.
-   **Impact on CLI**: The CLI command handlers will need to be updated to use the new package-level API.

This plan provides a roadmap for the refactoring effort. Specific implementation details will be worked out during the coding phase.