package utils

import (
	"crypto/rand"
	"fmt"

	// "io" // Removed unused import
	"math/big"

	"github.com/OG-Open-Source/PanelBase/internal/configuration"
)

// IDGenerator holds the configuration needed for generating IDs.
type IDGenerator struct {
	alphabet string
	length   int
}

// NewIDGenerator creates a new IDGenerator instance based on the security configuration.
func NewIDGenerator(cfg *configuration.SecurityConfig) (*IDGenerator, error) {
	if cfg == nil || cfg.Secrets.Alphabet == "" || cfg.Secrets.Length <= 0 {
		return nil, fmt.Errorf("invalid security config for ID generator: alphabet and length must be set")
	}
	return &IDGenerator{
		alphabet: cfg.Secrets.Alphabet,
		length:   cfg.Secrets.Length,
	}, nil
}

// generateRandomString generates a random string of the configured length using the configured alphabet.
// Uses crypto/rand for cryptographically secure random numbers.
func (g *IDGenerator) generateRandomString() (string, error) {
	alphabetLen := big.NewInt(int64(len(g.alphabet)))
	bytes := make([]byte, g.length)
	for i := range bytes {
		n, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random number for ID: %w", err)
		}
		bytes[i] = g.alphabet[n.Int64()]
	}
	return string(bytes), nil
}

// Generate generates an ID with the given prefix.
func (g *IDGenerator) Generate(prefix string) (string, error) {
	randomPart, err := g.generateRandomString()
	if err != nil {
		return "", err // Propagate the error
	}
	return prefix + "_" + randomPart, nil
}

// MustGenerate generates an ID with the given prefix and panics on error.
// Useful when ID generation failure is considered a fatal error.
func (g *IDGenerator) MustGenerate(prefix string) string {
	id, err := g.Generate(prefix)
	if err != nil {
		panic(fmt.Sprintf("failed to generate ID with prefix '%s': %v", prefix, err))
	}
	return id
}

// --- Convenience functions for specific prefixes ---

func (g *IDGenerator) UserID() (string, error) {
	return g.Generate("usr")
}

func (g *IDGenerator) ContainerID() (string, error) {
	return g.Generate("ctr")
}

func (g *IDGenerator) ThemeID() (string, error) {
	return g.Generate("thm")
}

func (g *IDGenerator) PluginID() (string, error) {
	return g.Generate("plg")
}

func (g *IDGenerator) TokenID() (string, error) {
	return g.Generate("tok")
}

func (g *IDGenerator) SessionID() (string, error) {
	return g.Generate("ses")
}

func (g *IDGenerator) CommandID() (string, error) {
	return g.Generate("cmd")
}

func (g *IDGenerator) RequestID() (string, error) {
	return g.Generate("req")
}
