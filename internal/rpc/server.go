package rpc

import (
	"fmt"
	"net"
	"net/rpc"

	// "os" // No longer needed for socket operations

	// "github.com/OG-Open-Source/PanelBase/internal/config" // No longer needed here
	"github.com/OG-Open-Source/PanelBase/internal/logger"
	"github.com/OG-Open-Source/PanelBase/internal/utils"
	// pkgLog "github.com/OG-Open-Source/PanelBase/pkg/service/v1" // Removed unused import alias
	// pkgID "github.com/OG-Open-Source/PanelBase/pkg/utils/v1"   // Removed unused import alias
)

// const rpcTCPAddress = "127.0.0.1:12345" // Removed hardcoded address

// --- Service Implementations (Wrappers around internal logic) ---

// IDServiceRPC provides the RPC implementation for the pkgID.IDService interface.
type IDServiceRPC struct {
	generator *utils.IDGenerator // Reference to the internal ID generator
}

// Generate implements the RPC method for IDService.
// Note: RPC methods must be exported (start with uppercase) and follow specific signatures.
// The first argument is the args struct, the second is the reply pointer. Both must be exported or built-in types.
func (s *IDServiceRPC) Generate(prefix string, reply *string) error {
	if s.generator == nil {
		return fmt.Errorf("ID generator not initialized in RPC service")
	}
	id, err := s.generator.Generate(prefix)
	if err != nil {
		return err // Propagate error
	}
	*reply = id
	return nil
}

// Convenience methods mapping (RPC methods need exported args/reply types or built-in)
// We use an empty struct `struct{}` as a placeholder for args when none are needed.
func (s *IDServiceRPC) UserID(args struct{}, reply *string) error { return s.Generate("usr", reply) }
func (s *IDServiceRPC) ContainerID(args struct{}, reply *string) error {
	return s.Generate("ctr", reply)
}
func (s *IDServiceRPC) ThemeID(args struct{}, reply *string) error   { return s.Generate("thm", reply) }
func (s *IDServiceRPC) PluginID(args struct{}, reply *string) error  { return s.Generate("plg", reply) }
func (s *IDServiceRPC) TokenID(args struct{}, reply *string) error   { return s.Generate("tok", reply) }
func (s *IDServiceRPC) SessionID(args struct{}, reply *string) error { return s.Generate("ses", reply) }
func (s *IDServiceRPC) CommandID(args struct{}, reply *string) error { return s.Generate("cmd", reply) }
func (s *IDServiceRPC) RequestID(args struct{}, reply *string) error { return s.Generate("req", reply) }

// LogServiceRPC provides the RPC implementation for the pkgLog.LogService interface.
type LogServiceRPC struct {
	appLogger *logger.Logger // Reference to the internal logger
}

// LogArgs holds arguments for the Log RPC method.
type LogArgs struct {
	Message string
}

// LogfArgs holds arguments for the Logf RPC method.
// Note: Using []interface{} directly with net/rpc's default gob encoding can be problematic.
type LogfArgs struct {
	Format string
	V      []interface{}
}

// Log implements the RPC method for LogService.
func (s *LogServiceRPC) Log(args LogArgs, reply *struct{}) error {
	if s.appLogger == nil {
		return fmt.Errorf("logger not initialized in RPC service")
	}
	s.appLogger.Log("(plugin) " + args.Message) // Prepend indication it's from a plugin
	return nil
}

// Logf implements the RPC method for LogService.
func (s *LogServiceRPC) Logf(args LogfArgs, reply *struct{}) error {
	if s.appLogger == nil {
		return fmt.Errorf("logger not initialized in RPC service")
	}
	// Format the message before logging
	formattedMsg := fmt.Sprintf(args.Format, args.V...)
	s.appLogger.Log("(plugin) " + formattedMsg)
	return nil
}

// Convenience methods removed as LogLevel is gone.

// --- RPC Server Setup ---

// StartRPCServer initializes and starts the RPC server listening on the given host and port.
// It signals on the ready channel once the server is ready to accept connections.
func StartRPCServer(appLogger *logger.Logger, idGen *utils.IDGenerator, host string, port int, ready chan<- struct{}) error {
	if appLogger == nil || idGen == nil {
		return fmt.Errorf("logger and id generator must be provided to start RPC server")
	}
	if ready == nil {
		return fmt.Errorf("ready channel cannot be nil")
	}
	if port <= 0 {
		return fmt.Errorf("invalid port provided: %d", port)
	}
	if host == "" {
		// Default to listening on localhost if host is empty,
		// as listening on 0.0.0.0 might expose RPC unnecessarily.
		host = "127.0.0.1"
		appLogger.Logf("RPC host not specified, defaulting to %s", host)
	}

	// Create RPC service instances
	idService := &IDServiceRPC{generator: idGen}
	logService := &LogServiceRPC{appLogger: appLogger}

	// Register the services
	err := rpc.RegisterName("IDService", idService) // Use specific names for clarity
	if err != nil {
		return fmt.Errorf("failed to register IDService for RPC: %w", err)
	}
	err = rpc.RegisterName("LogService", logService)
	if err != nil {
		// Attempt to unregister the first one if the second fails? Maybe not necessary.
		return fmt.Errorf("failed to register LogService for RPC: %w", err)
	}

	// Construct the RPC listen address (listen on all interfaces for simplicity, like the main server)
	// Use "0.0.0.0" or "" to listen on all available interfaces.
	// Use the provided host and port
	rpcListenAddr := fmt.Sprintf("%s:%d", host, port)

	// Listen on the TCP port
	listener, err := net.Listen("tcp", rpcListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on RPC TCP address '%s': %w", rpcListenAddr, err)
	}
	appLogger.Logf("RPC server listening on TCP: %s", rpcListenAddr) // Use Logf

	// Start accepting connections in a new goroutine
	go func() {
		defer listener.Close() // Ensure listener is closed when loop exits

		// Signal that the server is ready *before* blocking on Accept
		appLogger.Log("RPC server goroutine started, signaling ready...")
		ready <- struct{}{} // Send signal (empty struct uses no memory)
		close(ready)        // Close the channel after signaling

		// Now block and accept connections
		appLogger.Log("RPC server accepting connections...")       // Use Log
		rpc.Accept(listener)                                       // Blocks until listener is closed
		appLogger.Log("RPC server stopped accepting connections.") // Use Log (or maybe Errorf if unexpected?)
	}()

	// TODO: Implement graceful shutdown of the RPC server.

	return nil
}
