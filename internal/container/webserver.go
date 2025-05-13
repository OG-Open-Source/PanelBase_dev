package container

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	// "github.com/OG-Open-Source/PanelBase/internal/logger" // TODO: Inject logger later
)

const (
	templatesDir   = "templates"
	uiSettingsFile = "ui_settings.json" // Added constant for settings filename
)

// responseRecorder wraps http.ResponseWriter to capture status code and body.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

// WriteHeader captures the status code.
func (rec *responseRecorder) WriteHeader(code int) {
	rec.statusCode = code
	// Don't write header to underlying writer yet, wait until ServeHTTP decides.
}

// Write captures the body.
func (rec *responseRecorder) Write(b []byte) (int, error) {
	if rec.statusCode == 0 {
		rec.statusCode = http.StatusOK // Default if WriteHeader wasn't called
	}
	return rec.body.Write(b)
}

// flush writes the captured response to the original ResponseWriter.
func (rec *responseRecorder) flush() {
	if rec.statusCode != 0 {
		rec.ResponseWriter.WriteHeader(rec.statusCode)
	}
	rec.ResponseWriter.Write(rec.body.Bytes())
}

// containerWebHandler serves static files for a specific container's web directory,
// applying URL rewriting rules.
type containerWebHandler struct {
	webRootDir    string
	templatesPath string // Path to the templates directory (e.g., /path/to/container/web/templates)
	fileServer    http.Handler
	uiSettings    map[string]interface{} // Added field for UI settings
	// logger *logger.Logger // TODO: Add logger
}

// NewContainerWebHandler creates a new handler for serving a container's web content.
func NewContainerWebHandler(webRootDir string /*, logger *logger.Logger*/) (http.Handler, error) {
	// Ensure the web root directory exists
	webRootStat, err := os.Stat(webRootDir)
	if err != nil {
		if os.IsNotExist(err) {
			// It might be okay if it doesn't exist initially? For now, return error.
			return nil, err // Or fmt.Errorf("web root directory '%s' not found", webRootDir)
		}
		return nil, err // Other error accessing webRootDir
	}
	if !webRootStat.IsDir() {
		return nil, fmt.Errorf("web root '%s' is not a directory", webRootDir)
	}

	// Determine container root directory (parent of webRootDir)
	containerRootDir := filepath.Dir(webRootDir)

	// Load UI settings
	uiSettingsPath := filepath.Join(containerRootDir, uiSettingsFile)
	var loadedSettings map[string]interface{}

	settingsData, err := os.ReadFile(uiSettingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// logger.Warnf("ui_settings.json not found at %s, proceeding without UI settings.", uiSettingsPath) // TODO: Add logging
			loadedSettings = make(map[string]interface{}) // Use empty map if not found
		} else {
			// logger.Errorf("Error reading ui_settings.json at %s: %v. Proceeding without UI settings.", uiSettingsPath, err) // TODO: Add logging
			loadedSettings = make(map[string]interface{}) // Use empty map on other read errors
			// Optionally return error here if settings are critical: return nil, fmt.Errorf("error reading ui settings '%s': %w", uiSettingsPath, err)
		}
	} else {
		err = json.Unmarshal(settingsData, &loadedSettings)
		if err != nil {
			// logger.Errorf("Error parsing ui_settings.json at %s: %v. Proceeding with empty UI settings.", uiSettingsPath, err) // TODO: Add logging
			loadedSettings = make(map[string]interface{}) // Use empty map on unmarshal error
			// Optionally return error here: return nil, fmt.Errorf("error parsing ui settings '%s': %w", uiSettingsPath, err)
		}
		// logger.Infof("Successfully loaded UI settings from %s", uiSettingsPath) // TODO: Add logging
	}

	templatesPath := filepath.Join(webRootDir, templatesDir)
	// Ensure templates directory exists (optional, handleErrorPage checks files)
	// os.MkdirAll(templatesPath, 0755)

	return &containerWebHandler{
		webRootDir:    webRootDir,
		templatesPath: templatesPath,
		fileServer:    http.FileServer(http.Dir(webRootDir)), // Base file server
		uiSettings:    loadedSettings,                        // Assign loaded settings
		// logger: logger,
	}, nil
}

// ServeHTTP implements the http.Handler interface.
func (h *containerWebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get the clean path (removes '..' etc.)
	reqPath := path.Clean(r.URL.Path)
	fsPath := filepath.Join(h.webRootDir, filepath.FromSlash(reqPath)) // Map URL path to filesystem path

	// --- Check if the request targets an HTML/HTM file (directly or implicitly) ---
	servePath := "" // The actual filesystem path to serve (potentially with .html/.htm added)
	isHTML := false

	// 1. Check if the direct path exists
	info, err := os.Stat(fsPath)
	if err == nil {
		if info.IsDir() {
			// If it's a directory, check for index.html or index.htm
			indexPath := filepath.Join(fsPath, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				servePath = indexPath
				isHTML = true
			} else {
				indexPath = filepath.Join(fsPath, "index.htm")
				if _, err := os.Stat(indexPath); err == nil {
					servePath = indexPath
					isHTML = true
				}
			}
			// If neither index file exists, let the file server handle directory listing (or 403) later
		} else {
			// It's a file, check if it's HTML/HTM
			if strings.HasSuffix(strings.ToLower(fsPath), ".html") || strings.HasSuffix(strings.ToLower(fsPath), ".htm") {
				servePath = fsPath
				isHTML = true
			} else {
				// It's another type of file (CSS, JS, image etc.)
				servePath = fsPath
				isHTML = false
			}
		}
	} else if os.IsNotExist(err) {
		// 2. If direct path doesn't exist, try adding .html/.htm (URL rewriting)
		htmlPath := fsPath + ".html"
		if _, err := os.Stat(htmlPath); err == nil {
			servePath = htmlPath
			isHTML = true
		} else {
			htmPath := fsPath + ".htm"
			if _, err := os.Stat(htmPath); err == nil {
				servePath = htmPath
				isHTML = true
			}
		}
		// If still not found after adding extensions, servePath remains empty, will result in 404 later
	} else {
		// Other error accessing the path (e.g., permission denied)
		// h.logger.Errorf("Error accessing path %s: %v", fsPath, err) // TODO: Add logging
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// --- Serve Content ---

	if isHTML && servePath != "" {
		// Render HTML template
		// h.logger.Infof("Rendering HTML template %s for request %s", servePath, reqPath) // TODO: Add logging
		content, err := os.ReadFile(servePath)
		if err != nil {
			// h.logger.Errorf("Error reading HTML file %s: %v", servePath, err) // TODO: Add logging
			// Try serving a 500 error page template
			if !h.handleErrorPage(w, http.StatusInternalServerError) {
				// Fallback if error page fails
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
			return
		}

		tmplName := filepath.Base(servePath)
		tmpl, err := template.New(tmplName).Parse(string(content))
		if err != nil {
			// h.logger.Errorf("Error parsing HTML template %s: %v", servePath, err) // TODO: Add logging
			if !h.handleErrorPage(w, http.StatusInternalServerError) {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
			return
		}

		data := make(map[string]interface{})
		// Merge uiSettings into data
		if h.uiSettings != nil {
			for key, value := range h.uiSettings {
				data[key] = value
			}
		}
		// Add other context data if needed
		// data["request_path"] = r.URL.Path

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Note: WriteHeader(http.StatusOK) is implicitly called by the first Write
		err = tmpl.Execute(w, data)
		if err != nil {
			// h.logger.Errorf("Error executing HTML template %s: %v", servePath, err) // TODO: Add logging
			// Don't try to write another error if Execute already started writing
		}
		return // HTML rendered (or error occurred during rendering)
	}

	// --- If not HTML or template rendering failed, use FileServer (with error handling) ---
	// Wrap the response writer to capture the status code
	recorder := &responseRecorder{
		ResponseWriter: w,
		statusCode:     0,
		body:           new(bytes.Buffer),
	}

	// Let the file server handle non-HTML files, directories without index.html, or if servePath is empty (original 404 case)
	h.fileServer.ServeHTTP(recorder, r)

	// If the file server returned an error status code (e.g., 404, 403), try handling it with custom error pages
	if recorder.statusCode >= 400 {
		// h.logger.Warnf("File server returned status %d for %s, attempting error page.", recorder.statusCode, reqPath) // TODO: Add logging
		if h.handleErrorPage(w, recorder.statusCode) {
			return // Custom error page was served
		}
	}

	// If no custom error page was served (or status was OK), flush the original response from the file server
	recorder.flush()
}

// handleErrorPage attempts to serve a custom error page template.
// Returns true if a custom page was successfully served, false otherwise.
func (h *containerWebHandler) handleErrorPage(w http.ResponseWriter, code int) bool { // Removed unused 'r' parameter
	// Define template search order
	baseName := strconv.Itoa(code)
	candidates := []string{
		baseName + ".html",
		baseName + ".htm",
		"error.html",
		"error.htm",
	}

	var foundTemplatePath string
	// var foundConflict bool // Commented out as unused for now

	// Check for conflicts and find the first existing template
	htmlExists := false
	htmExists := false
	errHtmlExists := false
	errHtmExists := false

	for _, candidate := range candidates {
		p := filepath.Join(h.templatesPath, candidate)
		_, err := os.Stat(p)
		if err == nil { // File exists
			if foundTemplatePath == "" { // Found the first one in order of preference
				foundTemplatePath = p
			}
			// Check for conflicts specifically for the status code
			if candidate == baseName+".html" {
				htmlExists = true
			}
			if candidate == baseName+".htm" {
				htmExists = true
			}
			// Check for conflicts for the generic error page
			if candidate == "error.html" {
				errHtmlExists = true
			}
			if candidate == "error.htm" {
				errHtmExists = true
			}
		}
	}

	// Check for conflicts
	if (htmlExists && htmExists) || (errHtmlExists && errHtmExists) {
		// conflictMsg := fmt.Sprintf("Conflicting error templates found for status %d or generic error in %s", code, h.templatesPath) // Commented out as unused for now
		// h.logger.Errorf(conflictMsg) // TODO: Add logging
		http.Error(w, "Internal Server Error: Conflicting error page templates.", http.StatusInternalServerError)
		return true // We handled it by serving a 500
	}

	// If a template was found, try to parse and execute it
	if foundTemplatePath != "" {
		tmpl, err := template.ParseFiles(foundTemplatePath)
		if err != nil {
			// h.logger.Errorf("Error parsing error template %s: %v", foundTemplatePath, err) // TODO: Add logging
			// Fall through to default plain text error
		} else {
			data := map[string]interface{}{
				"http_status_code":    code,
				"http_status_message": http.StatusText(code),
			}
			// Merge uiSettings into data, ensuring system variables are not overwritten by uiSettings
			if h.uiSettings != nil {
				for key, value := range h.uiSettings {
					if _, exists := data[key]; !exists { // Only add if key doesn't already exist (e.g. http_status_code)
						data[key] = value
					}
				}
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(code) // Write the correct status code
			err = tmpl.Execute(w, data)
			if err != nil {
				// h.logger.Errorf("Error executing error template %s: %v", foundTemplatePath, err) // TODO: Add logging
				// Attempt to send a plain text error if execution failed mid-way
				// This might write partial content, which isn't ideal.
				// Consider buffering the template execution first.
				// For now, just log and indicate we tried but failed.
				// The client might receive a partial response + the plain text error.
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

			}
			return true // We attempted to handle it with a template
		}
	}

	// If no template found or template failed, return false (caller should handle default)
	return false
}

// TODO: Implement starting/stopping the actual HTTP server for the container.
// TODO: Integrate ui_settings.json into template data.
