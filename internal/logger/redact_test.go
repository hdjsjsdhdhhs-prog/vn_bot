package logger

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Registry tests are serial; restore state before parallel package tests resume.
func isolateSecrets(t *testing.T) {
	t.Helper()
	secretMu.Lock()
	previous := logSecrets
	logSecrets = nil
	secretMu.Unlock()
	t.Cleanup(func() {
		secretMu.Lock()
		logSecrets = previous
		secretMu.Unlock()
	})
}

func TestRedactSecrets_EncodingsOverlapAndRepeatedSanitization(t *testing.T) {
	const secret = "fixture +/\"token"
	quoted := strconv.Quote(secret)
	for _, encoded := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), quoted[1:len(quoted)-1]} {
		input := "before " + encoded + " after " + encoded
		got := RedactSecrets(input, "", secret)
		assert.Equal(t, "before [redacted] after [redacted]", got)
		assert.Equal(t, got, RedactSecrets(got, secret))
	}
	assert.Equal(t, "[redacted] [redacted]", RedactSecrets("abcdef abc", "abc", "abcdef"))
	assert.Equal(t, "[redacted]", RedactSecrets("red", "red", "red"))
	assert.Equal(t, "[redacted]", RedactSecrets("[redacted]", "red"))
	assert.Equal(t, "diagnostic", RedactSecrets("diagnostic", ""))
}

func TestRegisterSecrets_ConcurrentAndIdempotent(t *testing.T) {
	isolateSecrets(t)
	RegisterSecrets()
	RegisterSecrets("", "startup-fixture")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				RegisterSecrets("startup-fixture", "", "node-fixture")
				if got := Sanitize("failed startup-fixture node-fixture"); got != "failed [redacted] [redacted]" {
					t.Errorf("unexpected diagnostic: %q", got)
				}
			}
		}()
	}
	wg.Wait()
	secretMu.RLock()
	values := append([]string(nil), logSecrets...)
	secretMu.RUnlock()
	assert.ElementsMatch(t, []string{"startup-fixture", "node-fixture"}, values)
}

func TestSanitize_URLsAndSafeURLIdempotency(t *testing.T) {
	isolateSecrets(t)
	for _, input := range []string{
		`Get "https://user:pass@example.test/sub/customer?token=value": timeout`,
		`redirect https://redirect.test/sub/customer failed`,
		`Get "https://example.test/path with spaces/customer": invalid URL`,
	} {
		got := Sanitize(input)
		assert.NotContains(t, got, "customer")
		assert.NotContains(t, got, "example.test")
		assert.Contains(t, got, redacted)
		assert.Equal(t, got, Sanitize(got))
	}
	for _, input := range []string{"", "https://example.test/sub/customer", "/sub/customer", "%invalid", redacted} {
		assert.Equal(t, SafeURL(input), SafeURL(SafeURL(input)))
	}
	assert.Empty(t, SafeURL(""))
}

func TestSafeError_PreservesOriginalChainAndOnlyChangesLogRepresentation(t *testing.T) {
	isolateSecrets(t)
	cause := errors.New("customer-fixture failed")
	urlErr := &url.Error{Op: "Get", URL: "https://example.test/sub/customer-fixture", Err: cause}
	wrapped := fmt.Errorf("fetch node: %w", urlErr)
	original := wrapped.Error()
	field := SafeError(wrapped, "customer-fixture")
	assert.Equal(t, zapcore.StringType, field.Type)
	assert.Contains(t, field.String, "fetch node: Get")
	assert.Contains(t, field.String, "failed")
	assert.NotContains(t, field.String, "customer-fixture")
	assert.NotContains(t, field.String, "example.test")
	assert.Equal(t, original, wrapped.Error())
	assert.ErrorIs(t, wrapped, cause)
	var actual *url.Error
	require.ErrorAs(t, wrapped, &actual)
	assert.Same(t, urlErr, actual)
	assert.Equal(t, zapcore.SkipType, SafeError(nil).Type)
	secretMu.RLock()
	assert.Empty(t, logSecrets, "per-call secrets must not enter the registry")
	secretMu.RUnlock()
}

func TestLoggerRedaction_ComposedFieldsAndAdapters(t *testing.T) {
	isolateSecrets(t)
	RegisterSecrets("server-fixture")
	core, logs := observer.New(zapcore.DebugLevel)
	previous := Log
	Log = zap.New(core)
	t.Cleanup(func() { Log = previous })
	svc := &Service{log: Log}
	fields := []zap.Field{
		zap.String("detail", "failed server-fixture"),
		zap.NamedError("cause", errors.New("failure server-fixture")),
		zap.String("url", SafeURL("https://example.test/private")),
		zap.String("sub_id", "customer-fixture"),
		zap.ByteString("body", []byte("private body")),
		zap.Int("subscription_id", 42),
	}
	svc.With(fields...).Info("service server-fixture")
	Info("global server-fixture", fields...)
	_, err := Writer().Write([]byte("stdlib server-fixture\n"))
	require.NoError(t, err)
	adapter := &tgbotapiLogger{}
	adapter.Printf("telegram %s", "server-fixture")
	adapter.Println("telegram server-fixture")
	entries := logs.All()
	require.Len(t, entries, 5)
	for _, entry := range entries {
		assert.NotContains(t, entry.Message, "server-fixture")
	}
	for _, entry := range entries[:2] {
		context := entry.ContextMap()
		assert.Equal(t, "failed [redacted]", context["detail"])
		assert.Equal(t, "failure [redacted]", context["cause"])
		assert.Equal(t, redacted, context["url"])
		assert.Equal(t, redacted, context["sub_id"])
		assert.Equal(t, redacted, context["body"])
		assert.EqualValues(t, 42, context["subscription_id"])
	}
	assert.True(t, strings.Contains(fields[0].String, "server-fixture"), "caller-owned fields must not change")
	assert.Equal(t, zapcore.ErrorType, fields[1].Type)
}
