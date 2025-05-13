# Theme Command Logging Design

This document outlines the logging design for the `themes` subcommands in PanelBase.

## General Logging Principles

- Timestamp Format: `YYYY-MM-DDTHH:MM:SSZ` (UTC) is used for all internal logs.
- Structure: Logs typically start with a timestamp, followed by a descriptive message. Variables within messages are often enclosed in single quotes for clarity (e.g., 'MyTheme', '1.0.0', 'thm_abc123', 'ext/themes/thm_abc123').
- CLI vs. Internal Logs: CLI commands might also print user-facing messages to STDOUT/STDERR, which are distinct from these internal application logs.

## `themes install <source>`

### Success Scenarios & Key Log Points:

1.  **Initiation & Definition Processing:**
    - `Fetching definition from source.`
    - `Processing definition.`
    - `Starting installation for theme '[THEME_NAME]' (v[THEME_VERSION]).`
    - `  Source: '[SOURCE_PATH_OR_URL]'`
    - `  Force: [true/false]`
2.  **Local Status Check & Decision Logic:**
    - If installing a new theme (no existing version or different theme):
      - `Checking local status: Theme '[THEME_NAME]' (v[THEME_VERSION]) not found locally. Proceeding with new installation.`
      - Or if other versions of the same theme exist: `Checking local status: Other versions of theme '[THEME_NAME]' found. Installing specified version '[THEME_VERSION]'.`
    - If the exact version already exists and `force` is true:
      - `Checking local status: Theme '[THEME_NAME]' (v[THEME_VERSION]) already exists. Force mode enabled. Proceeding with re-installation.`
3.  **Directory Preparation:**
    - For a new installation: `Preparing directory '[TARGET_PATH]' for theme '[THEME_NAME]' (v[THEME_VERSION]).`
    - For a forced re-installation (overwrite): `Preparing directory '[TARGET_PATH]' for re-installation of theme '[THEME_NAME]' (v[THEME_VERSION]).`
4.  **Asset Downloading:**
    - `Downloading [N] assets for theme '[THEME_NAME]' (v[THEME_VERSION]):`
    - For each asset: `  [[CURRENT_FILE_NUMBER]/[TOTAL_FILES]] Downloading '[ASSET_RELATIVE_PATH]'...`
    - `All assets for theme '[THEME_NAME]' (v[THEME_VERSION]) downloaded.`
5.  **Finalization & State Update:**
    - (If `config.SaveThemesState` fails, a CRITICAL log is generated: `CRITICAL: Theme '[THEME_NAME]' files installed to '[TARGET_PATH]', but FAILED TO SAVE STATE to themes.json: [ERROR_DETAILS]. Manual correction may be needed.`)
    - `Theme '[THEME_NAME]' (v[THEME_VERSION]) installed to '[TARGET_PATH]'.` (This is the unified message for both new and forced overwrite installations).

### Failure Scenarios & Key Log Points:

- **Fetching Definition Fails:** Specific error from `fetchThemeYAML` (e.g., network error, file not found).
- **Parsing Definition Fails:**
  - `Definition processing error: [ERROR_DETAILS_FROM_YAML_UNMARSHAL]`
  - `Installation failed for theme from source '[SOURCE_PATH_OR_URL]'.`
- **Metadata Validation Fails:**
  - `Definition processing error: [VALIDATION_ERROR_DETAILS]`
  - `Installation failed for theme '[THEME_NAME]'.`
- **Theme Already Exists (and `force` is false):**
  - `Installation aborted for theme '[THEME_NAME]' (v[THEME_VERSION]): Version already exists at '[EXISTING_PATH]' and --force flag was not used.` (CLI exits based on this specific error without further app logs).
- **ID Generation Fails (for new theme directory):**
  - `Installation failed for theme '[THEME_NAME]'.` (Followed by error: `failed to generate unique directory name...`)
- **Directory Operations Fail (create, remove):**
  - `Installation failed for theme '[THEME_NAME]'.` (Followed by specific error like `failed to remove existing theme directory...` or `failed to create theme directory...`)
- **Asset Download Fails:**
  - During download: `  [[CURRENT_FILE_NUMBER]/[TOTAL_FILES]] Downloading '[ASSET_RELATIVE_PATH]'... Error: [HTTP_ERROR_OR_STATUS]`
  - Overall failure: `Installation failed for theme '[THEME_NAME]'.` (Followed by error: `failed to download theme structure...`)
- **Writing Local `theme.json` Fails:**
  - `Installation failed for theme '[THEME_NAME]'.` (Followed by error: `failed to write local theme.json...`)
- **Saving Global `themes.json` Fails (after files are installed):**
  - `CRITICAL: Theme '[THEME_NAME]' files installed to '[TARGET_PATH]', but FAILED TO SAVE STATE to themes.json: [ERROR_DETAILS]. Manual correction may be needed.`
  - `Installation failed for theme '[THEME_NAME]'.` (This log might follow the CRITICAL one).

## `themes update <theme_id>`

### Success Scenarios & Key Log Points:

1.  **Initiation & Current Status:**
    - `Attempting to update theme '[THEME_NAME]' (ID: [THEME_ID]).`
    - `  Current version: '[CURRENT_VERSION]'."`
    - `  Source: [SOURCE_LINK]`
2.  **Fetching & Processing Remote Definition:**
    - `Fetching definition from source.`
    - `Processing definition for theme '[THEME_NAME]'.` (Name from remote meta)
    - `  Remote version found: '[REMOTE_VERSION]'."`
3.  **Version Comparison & Decision:**
    - If already latest: `Theme '[THEME_NAME]' (ID: [THEME_ID]) is already at the latest version ('[VERSION]'). No update needed.`
    - If remote version is older: `Available version '[REMOTE_VERSION]' for theme '[THEME_NAME]' (ID: [THEME_ID]) is older than installed version '[CURRENT_VERSION]'. No update performed.`
    - If newer version available: `Newer version '[REMOTE_VERSION]' available. Proceeding with update from '[CURRENT_VERSION]'.`
4.  **Directory Preparation (if update proceeds):**
    - `Preparing directory '[TARGET_PATH]' for new version '[NEW_VERSION]'.` (Involves removing old files and creating the directory).
5.  **Asset Downloading (if update proceeds):**
    - `Downloading [N] assets for theme '[THEME_NAME]' (v[NEW_VERSION]):`
    - For each asset: `  [[CURRENT_FILE_NUMBER]/[TOTAL_FILES]] Downloading '[ASSET_RELATIVE_PATH]'...`
    - `All assets for theme '[THEME_NAME]' (v[NEW_VERSION]) downloaded.`
6.  **Finalization & State Update (if update proceeds):**
    - (If `config.SaveThemesState` fails, a CRITICAL log is generated).
    - `Theme '[THEME_NAME]' (ID: [THEME_ID]) updated successfully to version '[NEW_VERSION]'.` (Version is enclosed in single quotes).

### Failure Scenarios & Key Log Points:

- **Loading Global State Fails:** `Update failed for theme ID '[THEME_ID]'. Could not load themes state: [ERROR_DETAILS]`
- **Theme Not Found in State:** `Update failed for theme ID '[THEME_ID]'. Theme not found in state.`
- **Theme Lacks SourceLink:** `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Theme does not have a SourceLink.`
- **Fetching Remote Definition Fails:** `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Could not fetch definition: [FETCH_ERROR_DETAILS]`
- **Parsing Remote Definition Fails:** `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Could not parse definition: [PARSE_ERROR_DETAILS]`
- **Remote Metadata Validation Fails:** `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Invalid definition: [VALIDATION_ERROR_DETAILS]`
- **Directory Operations Fail (remove old, create new):**
  - `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Could not remove old directory: [ERROR_DETAILS]`
  - `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Could not create directory: [ERROR_DETAILS]`
- **Asset Download Fails:** `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Download structure error: [DOWNLOAD_ERROR_DETAILS]`
- **Writing Local `theme.json` Fails:** `Update failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Could not write local theme.json: [ERROR_DETAILS]`
- **Saving Global `themes.json` Fails (after files are updated):**
  - `CRITICAL: Theme '[THEME_NAME]' (ID: [THEME_ID]) files updated, but FAILED TO SAVE GLOBAL STATE to themes.json: [ERROR_DETAILS]. Manual correction may be needed.`

## `themes list [theme_id]`

This command primarily outputs structured data to STDOUT for the user. Internal logging is minimal and generally focuses on error conditions encountered while trying to retrieve data.

- **Error Loading Global State (when listing all themes):** If `config.LoadThemesState()` fails, an error is printed to STDERR by the CLI. The `config` package itself might log details of the failure.
- **Error Accessing Local Theme Data (when listing a specific theme):**
  - If local `theme.json` cannot be read: CLI prints error to STDERR. Internal log: `Error reading local theme metadata...`
  - If local `theme.json` cannot be parsed: CLI prints error to STDERR. Internal log: `Error parsing local theme metadata...`
- **Warning for Inconsistent State:**
  - `Warning: Theme ID '[THEME_ID]' found locally but not in global state (configs/themes.json). INSTALLED_AT and LAST_UPDATED are N/A.`

## `themes remove <theme_id>`

### Success Scenarios & Key Log Points:

1.  **Initiation:**
    - `Attempting to remove theme '[THEME_NAME]' (ID: [THEME_ID]).` (This log occurs after confirming the theme exists in the state and its name is retrieved).
2.  **Directory Removal Process:**
    - `Removing directory for theme '[THEME_NAME]' (ID: [THEME_ID]) at '[THEME_PATH]'.`
3.  **Finalization & Completion:**
    - (If `config.SaveThemesState` fails, a CRITICAL log is generated).
    - `Theme '[THEME_NAME]' (ID: [THEME_ID]) removed successfully.`

### Failure Scenarios & Key Log Points:

- **Theme Not Found in State:** `Removal failed for theme ID '[THEME_ID]'. Theme not found in state.`
- **Directory Removal Fails:** `Removal failed for theme '[THEME_NAME]' (ID: [THEME_ID]). Could not remove directory: [OS_ERROR_DETAILS]`
- **Saving Global `themes.json` Fails (after directory is removed):**
  - `CRITICAL: Theme '[THEME_NAME]' (ID: [THEME_ID]) directory removed, but FAILED TO UPDATE GLOBAL STATE in themes.json: [ERROR_DETAILS]. Manual correction may be needed.`

## `themes create <directory_path>`

This command is interactive for metadata input. Internal logging focuses on the file system operations and YAML generation. CLI provides most user feedback.

### Success Scenarios & Key Log Points:

1.  **Initiation & Directory Validation:** (Primarily CLI output; internal logs for errors if `os.Stat` fails on the path).
    - CLI: `Scanning directory: [ABSOLUTE_DIRECTORY_PATH]`
2.  **Structure Scanning:**
    - `Scanning directory structure for theme at '[ABSOLUTE_DIRECTORY_PATH]' to generate theme.yaml.`
    - `Directory scan complete. Found [N] files/directories for structure.` (N is the count of items in the generated structure).
3.  **File Generation (after user provides metadata via CLI):**
    - `Generating 'theme.yaml' for theme '[THEME_NAME_FROM_USER_INPUT]' at '[PATH_TO_NEW_THEME_YAML]'`
    - `'theme.yaml' for theme '[THEME_NAME_FROM_USER_INPUT]' created successfully at '[PATH_TO_NEW_THEME_YAML]'`

### Failure Scenarios & Key Log Points:

- **Directory Validation Fails (not found, not a directory, access error):**
  - CLI prints error. Internal logs:
  - `Error for themes create: Directory '[USER_PROVIDED_PATH]' not found.`
  - `Error for themes create: '[USER_PROVIDED_PATH]' is not a directory.`
  - `Error accessing '[USER_PROVIDED_PATH]' for themes create: [OS_ERROR_DETAILS]`
- **Directory Structure Scanning Fails:**
  - `Failed to scan directory structure for '[ABSOLUTE_DIRECTORY_PATH]': [ERROR_DETAILS]`
- **Writing `theme.yaml` Fails:**
  - `Failed to write theme.yaml to '[PATH_TO_NEW_THEME_YAML]': [OS_ERROR_DETAILS]`
