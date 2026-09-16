package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	subscriptionTokenBytes = 32
	// SubscriptionTokenLength is the encoded length of a public subscription token.
	SubscriptionTokenLength = subscriptionTokenBytes * 2
)

// GenerateSubscriptionToken returns a 256-bit, URL-safe public subscription token.
func GenerateSubscriptionToken() (string, error) {
	random := make([]byte, subscriptionTokenBytes)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return "", fmt.Errorf("generate subscription token: %w", err)
	}

	return hex.EncodeToString(random), nil
}

// IsValidSubscriptionToken reports whether token has the canonical lowercase
// hexadecimal representation produced by GenerateSubscriptionToken.
func IsValidSubscriptionToken(token string) bool {
	if len(token) != SubscriptionTokenLength {
		return false
	}

	decoded := make([]byte, subscriptionTokenBytes)
	if _, err := hex.Decode(decoded, []byte(token)); err != nil {
		return false
	}

	return hex.EncodeToString(decoded) == token
}
