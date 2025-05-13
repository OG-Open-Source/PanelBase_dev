package container

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/OG-Open-Source/PanelBase/internal/logger"
	"github.com/OG-Open-Source/PanelBase/internal/utils"
)

const (
	containersDir     = "containers"
	containerMetaFile = "container.yaml" // Metadata filename
	minPort           = 1024
	maxPort           = 49151
)

// ContainerManager manages the lifecycle and state of containers.
type ContainerManager struct {
	idGen      *utils.IDGenerator
	containers map[string]*ContainerInfo // Map container ID to its runtime info
	mu         sync.RWMutex              // Mutex for thread-safe map access
	globalHost string                    // Global host from main config
	logger     *logger.Logger            // Added logger instance
}

// NewContainerManager creates a new ContainerManager instance.
func NewContainerManager(idGen *utils.IDGenerator, globalHost string, log *logger.Logger) (*ContainerManager, error) {
	if idGen == nil {
		return nil, fmt.Errorf("IDGenerator cannot be nil")
	}
	if log == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if globalHost == "" {
		globalHost = "0.0.0.0"
		log.Log("Warning: Global host for containers is empty, defaulting to 0.0.0.0")
	}

	// Ensure the base containers directory exists
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base containers directory '%s': %w", containersDir, err)
	}

	cm := &ContainerManager{
		idGen:      idGen,
		containers: make(map[string]*ContainerInfo),
		globalHost: globalHost,
		logger:     log,
	}
	// Load existing containers on startup
	cm.LoadExistingContainers()
	return cm, nil
}

// LoadExistingContainers scans the containers directory and loads metadata.
func (cm *ContainerManager) LoadExistingContainers() {
	cm.mu.Lock() // Lock for writing to the map
	defer cm.mu.Unlock()

	cm.logger.Logf("Scanning for existing containers in '%s'...", containersDir)
	dirs, err := os.ReadDir(containersDir)
	if err != nil {
		cm.logger.Logf("Error reading containers directory '%s': %v", containersDir, err)
		return
	}

	loadedCount := 0
	startAttemptCount := 0
	for _, dirEntry := range dirs {
		if !dirEntry.IsDir() {
			continue
		}
		containerID := dirEntry.Name()
		metaFilePath := filepath.Join(containersDir, containerID, containerMetaFile)

		metaData, err := os.ReadFile(metaFilePath)
		if err != nil {
			cm.logger.Logf("Skipping container '%s': failed to read %s: %v", containerID, containerMetaFile, err)
			continue
		}

		var meta ContainerMetadata
		err = yaml.Unmarshal(metaData, &meta)
		if err != nil {
			cm.logger.Logf("Skipping container '%s': failed to parse %s: %v", containerID, containerMetaFile, err)
			continue
		}

		// Validate metadata
		if !meta.IsValid() || meta.ID != containerID {
			cm.logger.Logf("Skipping container '%s': invalid metadata in %s (ID mismatch or invalid fields)", containerID, containerMetaFile)
			continue
		}

		// Create runtime info based on metadata
		info := &ContainerInfo{
			ID:     meta.ID,
			Status: StatusStopped, // Start as stopped, attempt start later if needed
			Port:   meta.Port,
			WebDir: filepath.Join(containersDir, containerID, "web"),
		}
		cm.containers[containerID] = info
		loadedCount++

		// If metadata indicates it should be running, attempt to start it (outside the lock later)
		if meta.Status == StatusRunning {
			startAttemptCount++
			// Need to call StartWebServer outside the lock to avoid deadlock
			// We'll collect IDs to start after the loop.
		}
	}

	cm.logger.Logf("Finished scanning containers. Loaded %d containers.", loadedCount)

	// Attempt to start containers marked as running (outside the initial lock)
	// This part needs careful consideration regarding locking if StartWebServer modifies the map.
	// For simplicity now, let's assume StartWebServer handles its own locking correctly.
	// We need to re-read the directory or store IDs to start. Let's store IDs.
	idsToStart := []string{}
	for _, dirEntry := range dirs {
		if !dirEntry.IsDir() {
			continue
		}
		containerID := dirEntry.Name()
		metaFilePath := filepath.Join(containersDir, containerID, containerMetaFile)
		metaData, err := os.ReadFile(metaFilePath)
		if err != nil {
			continue
		}
		var meta ContainerMetadata
		if yaml.Unmarshal(metaData, &meta) != nil {
			continue
		}
		if meta.IsValid() && meta.ID == containerID && meta.Status == StatusRunning {
			idsToStart = append(idsToStart, containerID)
		}
	}

	if len(idsToStart) > 0 {
		cm.logger.Logf("Attempting to restart %d containers marked as running...", len(idsToStart))
		for _, id := range idsToStart {
			// Use a goroutine to avoid blocking startup? Or start sequentially?
			// Starting sequentially for now.
			go func(containerID string) { // Use goroutine to avoid blocking main startup
				cm.logger.Logf("Attempting to start web server for container '%s' based on persisted status...", containerID)
				err := cm.StartWebServer(containerID) // StartWebServer needs to handle locking
				if err != nil {
					cm.logger.Logf("Failed to auto-start web server for container '%s': %v", containerID, err)
					// Update status to error in memory? Metadata update happens in Start/Stop
				}
			}(id)
		}
	}
}

// CreateContainer creates a new container instance, directory structure, and metadata file.
func (cm *ContainerManager) CreateContainer(name string, port int) (*ContainerInfo, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 1. Generate new container ID
	ctrID, err := cm.idGen.ContainerID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate container ID: %w", err)
	}
	if _, exists := cm.containers[ctrID]; exists {
		return nil, fmt.Errorf("generated container ID collision: %s", ctrID)
	}

	// 2. Create directory structure
	containerBasePath := filepath.Join(containersDir, ctrID)
	dirsToCreate := []string{
		filepath.Join(containerBasePath, "configs"),
		filepath.Join(containerBasePath, "plugins"),
		filepath.Join(containerBasePath, "commands"),
		filepath.Join(containerBasePath, "themes"),
		filepath.Join(containerBasePath, "web", "templates"),
	}
	for _, dir := range dirsToCreate {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create container directory '%s': %w", dir, err)
		}
	}

	// 3. Assign port if not provided or invalid
	assignedPort := port
	if assignedPort < minPort || assignedPort > maxPort {
		rand.Seed(time.Now().UnixNano())
		assignedPort = rand.Intn(maxPort-minPort+1) + minPort
		cm.logger.Logf("Port %d invalid or not specified for new container %s. Assigned random port: %d", port, ctrID, assignedPort)
	}

	// 4. Create and write metadata file
	meta := ContainerMetadata{
		ID:     ctrID,
		Name:   name, // Use provided name
		Port:   assignedPort,
		Status: StatusStopped, // Initial status is stopped
	}
	metaFilePath := filepath.Join(containerBasePath, containerMetaFile)
	if err := writeMetadata(metaFilePath, &meta); err != nil {
		// Attempt cleanup? Remove created directories?
		os.RemoveAll(containerBasePath)
		return nil, fmt.Errorf("failed to write container metadata file '%s': %w", metaFilePath, err)
	}

	// 5. Register the new container in memory
	info := &ContainerInfo{
		ID:     ctrID,
		Status: StatusStopped,
		Port:   assignedPort,
		WebDir: filepath.Join(containerBasePath, "web"),
	}
	cm.containers[ctrID] = info

	cm.logger.Logf("Created container '%s' (ID: %s) with port %d. Metadata saved.", name, ctrID, assignedPort)
	return info, nil
}

// writeMetadata marshals metadata to YAML and writes it to the specified path.
func writeMetadata(path string, meta *ContainerMetadata) error {
	data, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}
	return nil
}

// GetContainerInfo retrieves runtime information about a specific container.
func (cm *ContainerManager) GetContainerInfo(id string) (*ContainerInfo, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	info, exists := cm.containers[id]
	// Return a copy to prevent modification? For now, return pointer.
	return info, exists
}

// ListContainers returns a list of all managed container IDs.
func (cm *ContainerManager) ListContainers() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	ids := make([]string, 0, len(cm.containers))
	for id := range cm.containers {
		ids = append(ids, id)
	}
	return ids
}

// GetLoadedCount returns the number of containers currently loaded in memory.
func (cm *ContainerManager) GetLoadedCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.containers)
}

// assignPort is now handled within CreateContainer or loaded from metadata.
// func (info *ContainerInfo) assignPort() { ... } // Removed

// StartWebServer starts the dedicated HTTP server for the container and updates metadata.
func (cm *ContainerManager) StartWebServer(id string) error {
	cm.mu.Lock() // Lock for modifying container info map
	info, exists := cm.containers[id]
	if !exists {
		cm.mu.Unlock()
		return fmt.Errorf("container '%s' not found in memory", id)
	}

	if info.Status == StatusRunning && info.webServer != nil {
		cm.mu.Unlock()
		return fmt.Errorf("container '%s' web server is already running", id)
	}

	// Port should be loaded from metadata or assigned during creation
	if info.Port <= 0 {
		cm.mu.Unlock()
		// This shouldn't happen if loaded/created correctly, but handle defensively
		return fmt.Errorf("container '%s' has invalid port %d", id, info.Port)
	}
	addr := cm.globalHost + ":" + strconv.Itoa(info.Port)

	// Create the specific handler for this container
	handler, err := NewContainerWebHandler(info.WebDir) // Remove logger argument
	if err != nil {
		info.Status = StatusError
		info.LastError = fmt.Sprintf("Failed to create web handler: %v", err)
		// Update metadata status to error? Or just keep runtime error? Keep runtime for now.
		cm.mu.Unlock()
		return fmt.Errorf("failed to create web handler for container '%s': %w", id, err)
	}

	// Create and configure the HTTP server
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
		// TODO: Add timeouts
	}
	info.webServer = server
	info.Status = StatusRunning // Update runtime status
	info.LastError = ""
	cm.containers[id] = info // Update map
	cm.mu.Unlock()           // Unlock before blocking/goroutine and metadata write

	// Update persistent metadata status
	metaFilePath := filepath.Join(containersDir, id, containerMetaFile)
	if err := updateMetadataStatus(metaFilePath, StatusRunning); err != nil {
		// Log the error, but the server is already starting/started in memory.
		cm.logger.Logf("Warning: Failed to update container metadata status to running for '%s': %v", id, err)
	}

	cm.logger.Logf("Starting web server for container %s on %s", id, addr)
	go func() {
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			cm.logger.Logf("Error: Web server for container %s failed: %v", id, err)
			// Update runtime status on error
			cm.mu.Lock()
			info, exists := cm.containers[id]
			if exists {
				info.Status = StatusError
				info.LastError = fmt.Sprintf("Web server error: %v", err)
				info.webServer = nil
				cm.containers[id] = info
			}
			cm.mu.Unlock()
			// Also update persistent status to error? Or stopped? Let's set to stopped.
			updateMetadataStatus(metaFilePath, StatusStopped) // Or StatusError if we add it to metadata
		} else {
			cm.logger.Logf("Info: Web server for container %s stopped gracefully.", id)
		}
	}()

	return nil
}

// StopWebServer gracefully shuts down the HTTP server and updates metadata.
func (cm *ContainerManager) StopWebServer(id string) error {
	cm.mu.Lock() // Lock for modifying container info map
	info, exists := cm.containers[id]
	if !exists {
		cm.mu.Unlock()
		return fmt.Errorf("container '%s' not found in memory", id)
	}

	if info.Status != StatusRunning || info.webServer == nil {
		cm.mu.Unlock()
		// Allow stopping if status is Error? Maybe. For now, only stop Running.
		return fmt.Errorf("container '%s' web server is not running", id)
	}

	server := info.webServer // Get server instance

	// Update runtime status immediately
	info.Status = StatusStopped
	info.webServer = nil
	info.LastError = ""
	cm.containers[id] = info
	cm.mu.Unlock() // Unlock before blocking shutdown and metadata write

	// Update persistent metadata status
	metaFilePath := filepath.Join(containersDir, id, containerMetaFile)
	if err := updateMetadataStatus(metaFilePath, StatusStopped); err != nil {
		// Log the error, but proceed with shutdown.
		cm.logger.Logf("Warning: Failed to update container metadata status to stopped for '%s': %v", id, err)
	}

	cm.logger.Logf("Stopping web server for container %s...", id)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		cm.logger.Logf("Error: Graceful shutdown for container %s failed: %v", id, err)
		// Runtime status is already Stopped. Metadata status is already Stopped.
		return fmt.Errorf("graceful shutdown failed for container '%s': %w", id, err)
	}

	cm.logger.Logf("Web server for container %s stopped successfully.", id)
	return nil
}

// updateMetadataStatus reads, updates status, and writes back metadata.
func updateMetadataStatus(metaFilePath string, newStatus ContainerStatus) error {
	// Read existing metadata
	metaData, err := os.ReadFile(metaFilePath)
	if err != nil {
		return fmt.Errorf("failed to read metadata file '%s' for update: %w", metaFilePath, err)
	}

	var meta ContainerMetadata
	err = yaml.Unmarshal(metaData, &meta)
	if err != nil {
		return fmt.Errorf("failed to parse metadata file '%s' for update: %w", metaFilePath, err)
	}

	// Update status and write back
	meta.Status = newStatus
	return writeMetadata(metaFilePath, &meta)
}

// TODO: Implement functions for deleting containers (needs to stop server first, remove dir).
