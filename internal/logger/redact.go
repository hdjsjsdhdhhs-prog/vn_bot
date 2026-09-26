package logger

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const redacted = "[redacted]"

var (
	secretMu sync.RWMutex
	logSecrets []string
	// URLs in errors are not necessarily the original request URL (redirects).
	// Drop the entire URL, not selected path segments or query parameters.
	quotedURL = regexp.MustCompile(`(?i)"[a-z][a-z0-9+.-]*://[^"\r\n]*"`)
	inlineURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
)

// SafeURL deliberately hides the entire value, including malformed/relative
// URLs and userinfo. No part of a bearer URL is assumed to be public.
func SafeURL(raw string) string {
	if raw == "" {
		return ""
	}
	return redacted
}

// RedactSecrets returns a logging-only copy. Empty values are ignored, all
// occurrences are removed, and longer overlapping secrets are replaced first.
func RedactSecrets(s string, secrets ...string) string {
	values := make([]string, 0, len(secrets)*4)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		values = append(values, secret, url.QueryEscape(secret), url.PathEscape(secret))
		quoted := strconv.Quote(secret)
		values = append(values, quoted[1:len(quoted)-1])
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	// A single pass never re-processes inserted text. Preserve existing markers
	// too, since explicit SafeError fields also pass through sanitizeFields.
	pairs := []string{redacted, redacted}
	for _, value := range values {
		pairs = append(pairs, value, redacted)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// RegisterSecrets registers the finite set of server-side startup credentials,
// never per-request/customer tokens. Values live for the process lifetime and
// are used only for diagnostics, never to change returned errors. Repeated
// registration is idempotent; reads and writes share secretMu.
func RegisterSecrets(secrets ...string) {
	secretMu.Lock()
	defer secretMu.Unlock()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		found := false
		for _, registered := range logSecrets {
			if registered == secret {
				found = true
				break
			}
		}
		if !found {
			logSecrets = append(logSecrets, secret)
		}
	}
}

// Sanitize removes configured secrets and complete URLs from diagnostic text.
// It is not a sanitizer for arbitrary request/response bodies: never log those.
func Sanitize(s string) string {
	secretMu.RLock()
	s = RedactSecrets(s, logSecrets...)
	secretMu.RUnlock()
	s = quotedURL.ReplaceAllString(s, `"[redacted]"`)
	return inlineURL.ReplaceAllString(s, redacted)
}

// SafeError preserves useful Error() diagnostics without passing the original
// error to zap (which may also serialize Format/Unwrap via errorVerbose).
// The caller's error, including its errors.Is/As chain, is untouched.
func SafeError(err error, secrets ...string) zap.Field {
	if err == nil {
		return zap.Skip()
	}
	return zap.String("error", Sanitize(RedactSecrets(err.Error(), secrets...)))
}

// sanitizeFields is shared by the existing global and injected logger APIs.
// Copy the slice: callers may reuse fields, including concurrently.
func sanitizeFields(fields []zap.Field) []zap.Field {
	out := make([]zap.Field, len(fields))
	for i, field := range fields {
		out[i] = field
		key := strings.ToLower(field.Key)
		switch key {
		case "body", "body_preview", "raw_preview", "response_preview", "payload", "data",
			"authorization", "bearer", "api_token", "token", "bot_token", "password", "secret",
			"sub_id", "existing_sub_id", "client_id", "code":
			out[i] = zap.String(field.Key, redacted)
			continue
		case "subscription_id":
			// Numeric database IDs are diagnostics, string IDs are bearer values.
			if field.Type == zapcore.StringType {
				out[i] = zap.String(field.Key, redacted)
				continue
			}
		}
		switch field.Type {
		case zapcore.ErrorType:
			if err, ok := field.Interface.(error); ok {
				out[i] = SafeError(err)
				out[i].Key = field.Key
			}
		case zapcore.StringType:
			if strings.Contains(key, "url") {
				out[i] = zap.String(field.Key, SafeURL(field.String))
			} else {
				out[i] = zap.String(field.Key, Sanitize(field.String))
			}
		}
	}
	return out
}
