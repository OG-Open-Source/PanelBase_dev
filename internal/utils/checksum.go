package utils

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// CalculateFileSHA256 calculates the SHA256 checksum of the file at the given path.
// It normalizes CRLF to LF before computing the hash to ensure consistency across OS.
func CalculateFileSHA256(filePath string) (string, error) {
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return "", err // Return error if file cannot be read
	}
	// For very long files, you might want to log a snippet or hash of raw content instead of full length or content.
	// log.Printf("DEBUG_CHECKSUM: File: %s, Raw content (first 100 bytes): %s", filePath, string(fileData[:min(100, len(fileData))]))

	// Normalize line endings: CRLF -> LF
	normalizedData := bytes.ReplaceAll(fileData, []byte("\r\n"), []byte("\n"))
	// log.Printf("DEBUG_CHECKSUM: File: %s, Normalized content (first 100 bytes): %s", filePath, string(normalizedData[:min(100, len(normalizedData))]))

	hasher := sha256.New()
	// Write the normalized byte slice to the hasher.
	// err is not typically checked for hasher.Write as it always returns nil for hash.Hash implementations.
	// However, if it were to return an error, we'd want to propagate it.
	// For sha256.digest, Write always returns (len(p), nil).
	if _, err := hasher.Write(normalizedData); err != nil {
		// This case should ideally not be reached with standard sha256.
		return "", err
	}

	calculatedSum := hex.EncodeToString(hasher.Sum(nil))
	return calculatedSum, nil
}

// Helper function to avoid panic with slicing, not strictly needed if always checking length
// func min(a, b int) int {
// 	if a < b {
// 		return a
// 	}
// 	return b
// }