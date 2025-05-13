# CLI Logging and Output Optimization Plan

## 1. Objective

To refine the logging and console output behavior for theme-related CLI commands in PanelBase, based on user feedback. The goals are:
- Make logs from the `themes` package (`internal/extension/themes/manager.go`) more concise, direct, and ensure they include clear final status messages for operations.
- Remove redundant logging and standard output from the CLI command layer (`cmd/server/main.go`) for operational commands (install, update, remove, create), relying on the `themes` package logs for process details and final status.
- Retain essential user-facing error messages in the CLI layer.
- Retain the console output capability for query-type commands like `theme list`.

## 2. Affected Files

- `internal/extension/themes/manager.go`
- `cmd/server/main.go`

## 3. Detailed Steps

### Phase 1: Optimize Logging in `internal/extension/themes/manager.go`

**General Adjustments to `tm.logger.Logf` messages:**
1.  **Remove Prefixes**: Eliminate `Package API: ` or `Package API: [FunctionName] - ` prefixes from all log messages originating from package-level API functions (`Install`, `Update`, `Remove`, `Create`, `List`) and their primary helper methods.
2.  **Remove Internal Tags**: Avoid using internal classification tags like `Install - XXX` within log messages.
3.  **Optimize Wording**: Reduce repetition of words like "Successfully". Use more direct and varied phrasing (e.g., "Wrote..." or "Local file written: ..." instead of "Successfully wrote...").

**Specific Adjustments for Final Status Logs in API Functions:**

*   **`Install` function:**
    *   On successful installation, ensure the log message is: `Theme '[Name]' (v[Version]) installed to '[relativeInstallPath]'.`
*   **`Update` function:**
    *   On successful update: `Theme '[Name]' (ID: [ID]) updated to version '[Version]'.`
    *   If already latest: `Theme '[Name]' (ID: [ID]) is already at the latest version ('[Version]'). No update needed.`
    *   If remote version is older: `Available version '[RemoteVersion]' for theme '[Name]' (ID: [ID]) is older than installed version '[LocalVersion]'. No update performed.`
*   **`Remove` function:**
    *   On successful removal, log message changed to: `Theme '[Name]' (ID: [ID]) removed.`
*   **`Create` function:**
    *   On successful creation, log message changed to: `Theme '[Name]' (ID: [ID]) created from directory '[sourceDirectoryPath]' and registered to 'ext/themes/[newThemeID]'.`
*   **`List` function:**
    *   For error logs (e.g., `Failed to load themes state`), change `Package API List:` prefix to `List:`.

*(Specific line-by-line changes will be applied during the coding phase based on these guidelines.)*

### Phase 2: Modify CLI Commands in `cmd/server/main.go`

**For Operational Commands (`themeInstallCmd`, `themeUpdateCmd`, `themeRemoveCmd`, `themeCreateCmd`):**
1.  **Remove CLI-Level Logging**: Delete all `appLogger.Logf(...)` statements related to the theme operation's progress or outcome.
2.  **Remove CLI-Level Standard Output**: Delete all `fmt.Print/Printf/Println(...)` statements used for printing success messages or intermediate steps to `stdout`.
3.  **Retain Error Handling**: Keep the existing structure for handling errors returned by the `themes` package API:
    ```go
    if err != nil {
        fmt.Fprintf(os.Stderr, "User-friendly error message: %v\n", err) // Example
        os.Exit(1)
    }
    ```

**For Query-Type Command (`themeListCmd`):**
1.  **Retain Console Output for Results**: The existing logic that formats and prints the list of themes (when no arguments are given) or theme details (when a `theme_id` is provided) using `fmt.Print/Printf/Println(...)` will be **preserved**. This is considered essential for displaying query results directly to the user.
2.  **Remove Ancillary CLI-Level Logging**: If there are any `appLogger.Logf(...)` statements within `themeListCmd` that are not directly related to formatting the output (e.g., logging intermediate steps of the listing process itself, if any), they should be removed.

This plan aims to centralize detailed logging within the `themes` package while keeping the CLI layer focused on user interaction, error reporting, and displaying query results.