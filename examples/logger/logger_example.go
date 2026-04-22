package main

import (
	"errors"
	"fmt"

	"github.com/flowc-labs/flowc/pkg/logger"
)

func main() {
	fmt.Println("=== Envoy Logger Example ===")

	// Example 1: Default logger
	fmt.Println("\n1. Default Logger:")
	log1 := logger.NewDefaultEnvoyLogger()
	log1.Info("This is a default logger message")
	log1.WithField("component", "xds-server").Info("Server component started")

	// Example 2: JSON logger
	fmt.Println("\n2. JSON Logger:")
	log2 := logger.NewJSONLogger(logger.DebugLevel)
	log2.Debug("This is a debug message")
	log2.WithFields(map[string]any{
		"user":    "admin",
		"action":  "login",
		"success": true,
	}).Info("User action")

	// Example 3: Text logger
	fmt.Println("\n3. Text Logger:")
	log3 := logger.NewTextLogger(logger.InfoLevel)
	log3.Info("This is a text logger message")
	log3.WithError(errors.New("connection failed")).Error("Operation failed")

	// Example 4: File logger
	fmt.Println("\n4. File Logger:")
	fileLog, err := logger.NewFileLogger("/tmp/xds-server.log", logger.InfoLevel)
	if err != nil {
		log1.WithError(err).Error("Failed to create file logger")
	} else {
		fileLog.Info("This message will be written to /tmp/xds-server.log")
		fileLog.WithField("file", "/tmp/xds-server.log").Info("File logging example")
	}

	// Example 5: Custom configuration
	fmt.Println("\n5. Custom Configuration:")
	config := &logger.LoggerConfig{
		Type:  logger.TextLogger,
		Level: logger.DebugLevel,
	}
	customLog := logger.NewLogger(config)
	customLog.Debug("Debug message from custom logger")
	customLog.Info("Info message from custom logger")

	// Example 6: Logger with context
	fmt.Println("\n6. Logger with Context:")
	ctxLog := log1.WithFields(map[string]any{
		"request_id": "12345",
		"user_id":    "user123",
	})
	ctxLog.Info("Processing request")
	ctxLog.WithField("step", "validation").Debug("Validating input")

	// Example 7: Error handling
	fmt.Println("\n7. Error Handling:")
	err = errors.New("database connection failed")
	log1.WithError(err).Error("Failed to connect to database")

	// Example 8: Fatal logging (commented out to prevent exit)
	fmt.Println("\n8. Fatal Logging (commented out):")
	// log1.Fatal("This would exit the program") // Uncomment to see fatal behavior

	fmt.Println("\n=== Example Complete ===")
	fmt.Println("Check /tmp/xds-server.log for file logging output")
}
