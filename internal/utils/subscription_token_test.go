package utils

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSubscriptionToken(t *testing.T) {
	t.Parallel()

	const sampleSize = 1000
	tokens := make(map[string]struct{}, sampleSize)
	urlSafe := regexp.MustCompile(`^[a-f0-9]+$`)

	for range sampleSize {
		token, err := GenerateSubscriptionToken()
		require.NoError(t, err)
		assert.Len(t, token, SubscriptionTokenLength)
		assert.True(t, urlSafe.MatchString(token))
		assert.True(t, IsValidSubscriptionToken(token))
		_, duplicate := tokens[token]
		assert.False(t, duplicate)
		tokens[token] = struct{}{}
	}
}

func TestIsValidSubscriptionToken(t *testing.T) {
	t.Parallel()

	assert.False(t, IsValidSubscriptionToken(""))
	assert.False(t, IsValidSubscriptionToken("abc"))
	assert.False(t, IsValidSubscriptionToken(string(make([]byte, SubscriptionTokenLength))))
	assert.False(t, IsValidSubscriptionToken("A"+string(make([]byte, SubscriptionTokenLength-1))))
}
