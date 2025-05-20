package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCalculateFileSHA256(t *testing.T) {
	// Helper function to create a temporary file with specific content
	createTempFile := func(t *testing.T, content string, nameSuffix string) string {
		t.Helper()
		tmpFile, err := os.CreateTemp(t.TempDir(), "testfile_*"+nameSuffix)
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		if _, err := tmpFile.WriteString(content); err != nil {
			tmpFile.Close()
			t.Fatalf("Failed to write to temp file: %v", err)
		}
		if err := tmpFile.Close(); err != nil {
			t.Fatalf("Failed to close temp file: %v", err)
		}
		return tmpFile.Name()
	}

	tests := []struct {
		name          string
		fileContent   string
		expectedSum   string // Expected SHA256 sum in hex
		expectedError bool
		filePath      string // Use this if you want to test a non-existent file
	}{
		{
			name:        "empty file",
			fileContent: "",
			// SHA256 hash of an empty string is e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
			expectedSum:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expectedError: false,
		},
		{
			name:        "simple content",
			fileContent: "hello world",
			// SHA256 of "hello world" (assuming LF line ending if any, or no line ending)
			// For "hello world" (no newline): b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9
			// For "hello world\n": 2f05477fc24bb4fa04902b2a7c59930aa43f9808d4988a185080b8958957883d
			// Let's use "hello world" without newline for simplicity in this test case.
			expectedSum:   "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			expectedError: false,
		},
		{
			name:          "non-existent file",
			filePath:      filepath.Join(t.TempDir(), "nonexistentfile.txt"), // Ensure it doesn't exist
			expectedSum:   "",
			expectedError: true,
		},
		{
			name:        "content with LF line endings",
			fileContent: "line1\nline2\nline3",
			// sha256("line1\nline2\nline3")
			expectedSum:   "d12c8919980028542af033396a0a28709507680789658623359003055bd8150c",
			expectedError: false,
		},
		{
			name:        "content with CRLF line endings (should normalize to LF)",
			fileContent: "line1\r\nline2\r\nline3",
			// After normalization, this should be the same as sha256("line1\nline2\nline3")
			expectedSum:   "d12c8919980028542af033396a0a28709507680789658623359003055bd8150c",
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var filePathToTest string
			if tt.filePath != "" { // For testing non-existent file
				filePathToTest = tt.filePath
				// Ensure the file does not exist for this specific test case
				if tt.name == "non-existent file" {
					os.Remove(filePathToTest) // Attempt to remove if it somehow exists
				}
			} else {
				filePathToTest = createTempFile(t, tt.fileContent, "_"+strings.ReplaceAll(tt.name, " ", "_")+".txt")
			}

			sum, err := CalculateFileSHA256(filePathToTest)

			if tt.expectedError {
				if err == nil {
					t.Errorf("CalculateFileSHA256() error = %v, expectedError %v", err, tt.expectedError)
				}
			} else {
				if err != nil {
					t.Errorf("CalculateFileSHA256() unexpected error = %v", err)
					return
				}
				if sum != tt.expectedSum {
					t.Errorf("CalculateFileSHA256() sum = %v, want %v", sum, tt.expectedSum)
					// For debugging, print file content if sum mismatches
					if tt.filePath == "" { // only for created files
						contentBytes, _ := os.ReadFile(filePathToTest)
						t.Logf("File content for '%s': [%s]", tt.name, string(contentBytes))
						t.Logf("File content (hex) for '%s': [%x]", tt.name, contentBytes)
					}
				}
			}
		})
	}

	// Additional test to ensure CRLF and LF produce THE SAME hash after normalization
	t.Run("CRLF vs LF same hash after normalization", func(t *testing.T) {
		contentBase := "line one\nline two"

		// Create one file with LF and another with CRLF
		filePathLF := createTempFile(t, strings.ReplaceAll(contentBase, "\n", "\n"), "_lf_specific.txt")
		filePathCRLF := createTempFile(t, strings.ReplaceAll(contentBase, "\n", "\r\n"), "_crlf_specific.txt")

		sumLF, errLF := CalculateFileSHA256(filePathLF)
		if errLF != nil {
			t.Fatalf("Error calculating LF sum: %v", errLF)
		}

		sumCRLF, errCRLF := CalculateFileSHA256(filePathCRLF)
		if errCRLF != nil {
			t.Fatalf("Error calculating CRLF sum (after normalization): %v", errCRLF)
		}

		if sumLF != sumCRLF {
			t.Errorf("Expected SHA256 sums for LF and CRLF content to be THE SAME after normalization, but got different sums. LF: %s, CRLF (normalized): %s", sumLF, sumCRLF)
			
			// Log original file contents for debugging if sums differ
			contentLFBytes, _ := os.ReadFile(filePathLF)
			t.Logf("Original LF File content (hex): [%x]", contentLFBytes)
			contentCRLFBytes, _ := os.ReadFile(filePathCRLF)
			t.Logf("Original CRLF File content (hex): [%x]", contentCRLFBytes)
		}
	})

	t.Run("direct hash of LF bytes", func(t *testing.T) {
		inputBytes := []byte("line1\nline2\nline3")
		// Expected: d12c8919980028542af033396a0a28709507680789658623359003055bd8150c
		expectedSum := "d12c8919980028542af033396a0a28709507680789658623359003055bd8150c"
		
		hasher := sha256.New()
		hasher.Write(inputBytes)
		actualSum := hex.EncodeToString(hasher.Sum(nil))
		
		if actualSum != expectedSum {
			t.Errorf("Direct hash of LF bytes: got %s, want %s. Input hex: %x", actualSum, expectedSum, inputBytes)
		}
	})

	t.Run("direct hash of CRLF bytes", func(t *testing.T) {
		inputBytes := []byte("line1\r\nline2\r\nline3")
		// Expected: 8e6e1813a91673a97a507514725b09977399498094210059632869083090480a
		expectedSum := "8e6e1813a91673a97a507514725b09977399498094210059632869083090480a"

		hasher := sha256.New()
		hasher.Write(inputBytes)
		actualSum := hex.EncodeToString(hasher.Sum(nil))

		if actualSum != expectedSum {
			t.Errorf("Direct hash of CRLF bytes: got %s, want %s. Input hex: %x", actualSum, expectedSum, inputBytes)
		}
	})
}

func TestMinimalSHA256(t *testing.T) {
	input := []byte("hello world")
	// Standard SHA256 for "hello world" (no newline) is
	// b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9
	expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	hasher := sha256.New()
	hasher.Write(input)
	actual := hex.EncodeToString(hasher.Sum(nil))

	if actual != expected {
		t.Errorf("MinimalSHA256 failed: got %s, want %s for input '%s' (hex: %x)", actual, expected, string(input), input)
	}
}