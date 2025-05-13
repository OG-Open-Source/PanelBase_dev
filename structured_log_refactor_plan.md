# Structured Log Refactoring Plan for Themes Package

## 1. Objective

To refactor the logging within the `themes` package (`internal/extension/themes/manager.go`) to be more structured, readable, and concise, based on user feedback. This involves:
- Using indentation to represent main tasks and sub-tasks.
- Retaining timestamps for each log entry.
- Removing redundant or duplicate log messages.
- Ensuring the existing download progress log format (`[x/y] Downloading...`) is maintained and correctly indented.

## 2. Affected File

- `internal/extension/themes/manager.go`

## 3. General Principles for Log Refactoring

1.  **Indentation**:
    *   Main operational API functions (`Install`, `Update`, `Remove`, `Create`) will have a top-level starting log message.
    *   Logical sub-steps within these functions will be indented (e.g., using `"  "` for the first level of sub-tasks, `"    "` for the second, if necessary).
2.  **Clarity and Conciseness**:
    *   Log messages will be rephrased to be direct and informative.
    *   Redundant information or log lines that repeat the same status will be consolidated or removed.
3.  **Start and End Logs**:
    *   Each main operation should have a clear starting log message and a final status log message (e.g., "Theme X installed successfully...", "Update for Theme Y not needed...", "Theme Z removed.").
4.  **Error Logging**:
    *   Error logs should provide sufficient context and maintain an indentation level consistent with the step where the error occurred.
5.  **Helper Function Logs**:
    *   Logs from private helper functions (e.g., `_fetchAndValidateDefinition`, `_prepareDirectoryForInstall`) should either be:
        *   Integrated into the calling function's structured log flow (i.e., the caller logs the step, and the helper performs the action silently or returns detailed errors).
        *   Or, if helper logs are essential for detail, they must also follow the indentation rules relative to the caller's log structure.
    *   The detailed per-file download progress logs from `downloadAndSaveStructure` will be preserved and indented under a "Downloading assets..." parent log.

## 4. Proposed Log Structure Examples (Conceptual)

Indentation will be represented by `->` for one level and `-->` for two levels. Timestamps are omitted here for brevity but will be present in actual logs.

### For `Install` function:
```
Installing theme 'ThemeName' (vVersion) from 'source_details'...
-> Validating metadata...
--> Metadata validated.
-> Determining install action...
--> Action: [ActionType], Target ID: [theme_id_or_placeholder]
-> Preparing directory 'ext/themes/[theme_id]'...
--> Directory prepared.
-> Downloading assets ([N] total):
-->   [1/N] Downloading 'asset1.ext'...
-->   ...
-->   [N/N] Downloading 'assetN.ext'...
--> All [N] assets downloaded.
-> Finalizing installation:
--> Local theme.json written.
--> Global themes state updated.
Theme 'ThemeName' (vVersion) installed to 'ext/themes/[theme_id]'.
```

### For `Update` function:
```
Updating theme 'ThemeName' (ID: [theme_id])...
-> Current version: '[cv]', Source: '[source_url]'
-> Fetching and validating remote definition...
--> Fetched: '[RemoteThemeName]' (v[RemoteVersion]) from source.
-> Comparing versions...
--> (Log message for: newer version found, or already latest, or remote is older)
(If proceeding with update):
-> Preparing local directory '[path]' for update to v[NewVersion]...
--> Directory prepared.
-> Downloading assets ([N] total):
-->   [1/N] Downloading 'asset1.ext'...
-->   ...
-->   [N/N] Downloading 'assetN.ext'...
--> All [N] assets downloaded.
-> Finalizing update:
--> Local theme.json written for new version '[NewVersion]'.
--> Global themes state updated to version '[NewVersion]'.
Theme 'ThemeName' (ID: [theme_id]) updated successfully to version '[NewVersion]'.
(Else, if no update needed):
Theme 'ThemeName' (ID: [theme_id]) is already at the latest version ('[Version]'). No update needed.
```

### For `Remove` function:
```
Removing theme 'ThemeName' (ID: [theme_id])...
-> Removing from state file...
--> Removed from state file.
-> Deleting directory 'ext/themes/[theme_id]'...
--> Directory deleted. (or error if it fails)
Theme 'ThemeName' (ID: [theme_id]) removed.
```

### For `Create` function:
```
Creating theme 'ThemeName' from directory '[dirPath]'...
-> Scanning directory structure...
--> Structure scanned.
-> Validating provided metadata...
--> Metadata validated.
-> Preparing target directory 'ext/themes/[new_theme_id]'...
--> Directory prepared.
-> Copying files from source to 'ext/themes/[new_theme_id]'...
--> Files copied.
-> Finalizing theme creation:
--> Local theme.json written.
--> Global themes state updated for new theme (ID: [new_theme_id]).
Theme 'ThemeName' (ID: [new_theme_id]) created from directory '[dirPath]' and registered to 'ext/themes/[new_theme_id]'.
```

## 5. Implementation Notes
- A consistent indentation string (e.g., `"  "`) will be used.
- Each `tm.logger.Logf` call in the relevant functions will be reviewed and modified to fit this structured approach. This involves changing the format string to include leading spaces for indentation and rephrasing the message content.
- Care will be taken to ensure that error messages also provide clear context within this new structure.

This refactoring will involve a significant number of changes to log statements throughout `internal/extension/themes/manager.go`.