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

	// Normalize line endings: CRLF -> LF
	normalizedData := bytes.ReplaceAll(fileData, []byte("\r\n"), []byte("\n"))

	hasher := sha256.New()
	// Write the normalized byte slice to the hasher.
	// err is not typically checked for hasher.Write as it always returns nil for hash.Hash implementations.
	// However, if it were to return an error, we'd want to propagate it.
	// For sha256.digest, Write always returns (len(p), nil).
	if _, err := hasher.Write(normalizedData); err != nil {
		// This case should ideally not be reached with standard sha256.
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}