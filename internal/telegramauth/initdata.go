// Package telegramauth verifies Telegram Mini App initData, never initDataUnsafe.
package telegramauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxAge bounds replay of the read-only API credential. Clients must reopen
	// the Mini App when it expires; there is no sliding session refresh.
	MaxAge = 5 * time.Minute
	// FutureSkew allows small clock differences, not future-dated credentials.
	FutureSkew = 30 * time.Second
	// MaxInitDataBytes bounds parsing/HMAC work before handling untrusted data.
	MaxInitDataBytes = 16 * 1024
)

var (
	ErrInvalidInitData = errors.New("invalid Telegram authentication")
	ErrNotConfigured   = errors.New("Telegram authentication unavailable")
)

// Validator holds only the derived HMAC key. Construct once from the existing
// bot configuration. Neither the validator nor its input should be logged.
type Validator struct {
	key []byte
}

// New binds validation to this bot, without calling the Telegram API.
func New(botToken string) *Validator {
	if strings.TrimSpace(botToken) == "" {
		return &Validator{}
	}
	mac := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = mac.Write([]byte(botToken))
	return &Validator{key: mac.Sum(nil)}
}

// Verify returns only the authenticated Telegram user ID. All failures are
// sanitized and carry no raw input or parser errors. HMAC procedure:
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func (v *Validator) Verify(raw string, now time.Time) (int64, error) {
	if v == nil || len(v.key) == 0 {
		return 0, ErrNotConfigured
	}
	if raw == "" || len(raw) > MaxInitDataBytes {
		return 0, ErrInvalidInitData
	}
	fields, err := url.ParseQuery(raw)
	if err != nil || len(fields) < 3 {
		return 0, ErrInvalidInitData
	}
	// Reject ambiguous representations rather than selecting the first/last
	// duplicate. Bare/empty query components are not Telegram field pairs.
	for _, pair := range strings.Split(raw, "&") {
		if !strings.Contains(pair, "=") {
			return 0, ErrInvalidInitData
		}
	}
	keys := make([]string, 0, len(fields)-1)
	for key, values := range fields {
		if key == "" || len(values) != 1 || strings.ContainsAny(key, "=\r\n\x00") || strings.ContainsAny(values[0], "\r\n\x00") {
			return 0, ErrInvalidInitData
		}
		if key != "hash" {
			// Include unknown fields and signature. Excluding signature belongs
			// to Telegram's separate third-party Ed25519 procedure, not HMAC.
			keys = append(keys, key)
		}
	}
	hash, err := hex.DecodeString(fields.Get("hash"))
	if err != nil || len(hash) != sha256.Size {
		return 0, ErrInvalidInitData
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+fields.Get(key))
	}
	mac := hmac.New(sha256.New, v.key)
	_, _ = mac.Write([]byte(strings.Join(lines, "\n")))
	if !hmac.Equal(hash, mac.Sum(nil)) {
		return 0, ErrInvalidInitData
	}

	date := fields.Get("auth_date")
	seconds, err := strconv.ParseInt(date, 10, 64)
	if err != nil || seconds <= 0 || strconv.FormatInt(seconds, 10) != date {
		return 0, ErrInvalidInitData
	}
	// Compare Unix seconds directly; do not multiply untrusted values into a
	// time.Duration (overflow could turn an ancient date into a fresh one).
	if seconds < now.Unix()-int64(MaxAge/time.Second) || seconds > now.Unix()+int64(FutureSkew/time.Second) {
		return 0, ErrInvalidInitData
	}
	return userID(fields.Get("user"))
}

func userID(raw string) (int64, error) {
	if !json.Valid([]byte(raw)) {
		return 0, ErrInvalidInitData
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return 0, ErrInvalidInitData
	}
	var id int64
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0, ErrInvalidInitData
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return 0, ErrInvalidInitData
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, ErrInvalidInitData
		}
		switch name {
		case "id":
			if err := json.Unmarshal(value, &id); err != nil {
				return 0, ErrInvalidInitData
			}
		case "is_bot":
			if string(value) != "false" {
				return 0, ErrInvalidInitData
			}
		}
	}
	// Telegram documents user IDs as positive integers with at most 52 bits.
	// Decode the exact lowercase id field, never receiver/chat/username.
	if id <= 0 || id > (1<<52)-1 {
		return 0, ErrInvalidInitData
	}
	return id, nil
}
