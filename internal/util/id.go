// Package util contains small, domain-specific helpers shared by internal packages.
package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ValidID reports whether an identifier is safe for use as a runtime object name.
func ValidID(value string) bool { return validID.MatchString(value) }

// NewID creates a random identifier suitable for runtime-owned directory names.
func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate runtime ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
