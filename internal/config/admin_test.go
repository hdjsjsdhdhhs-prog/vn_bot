package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func adminTestConfig(t *testing.T) Config {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-only-password"), AdminMinBcryptCost)
	require.NoError(t, err)
	return Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: "https://example.com"}
}

func TestAdminConfiguration_Disabled(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {SiteURL: "http://localhost"}} {
		enabled, origin, err := cfg.AdminConfiguration()
		require.NoError(t, err)
		assert.False(t, enabled)
		assert.Empty(t, origin)
	}
}

func TestAdminConfiguration_Credentials(t *testing.T) {
	cfg := adminTestConfig(t)
	tests := []struct {
		name     string
		username string
		hash     string
		valid    bool
	}{
		{"valid", cfg.AdminUsername, cfg.AdminPasswordHash, true},
		{"allowed username", "Admin_1.name@example-2", cfg.AdminPasswordHash, true},
		{"maximum username", strings.Repeat("a", 64), cfg.AdminPasswordHash, true},
		{"missing username", "", cfg.AdminPasswordHash, false},
		{"missing hash", cfg.AdminUsername, "", false},
		{"blank username", " ", cfg.AdminPasswordHash, false},
		{"long username", strings.Repeat("a", 65), cfg.AdminPasswordHash, false},
		{"username newline", "admin\n", cfg.AdminPasswordHash, false},
		{"plaintext", cfg.AdminUsername, "not-a-bcrypt-hash", false},
		{"truncated", cfg.AdminUsername, cfg.AdminPasswordHash[:59], false},
		{"trailing newline", cfg.AdminUsername, cfg.AdminPasswordHash + "\n", false},
		{"noncanonical salt", cfg.AdminUsername, cfg.AdminPasswordHash[:28] + "B" + cfg.AdminPasswordHash[29:], false},
		{"noncanonical digest", cfg.AdminUsername, cfg.AdminPasswordHash[:59] + "B", false},
	}
	for _, minor := range []string{"a", "b", "y", "x"} {
		tests = append(tests, struct {
			name, username, hash string
			valid                bool
		}{
			"version " + minor, cfg.AdminUsername, "$2" + minor + cfg.AdminPasswordHash[3:], minor != "x",
		})
	}
	for _, cost := range []int{4, 9, 10, 11, 12, 13, 14, 15, 31} {
		// Cost validation parses the header; no expensive password comparison is needed.
		tests = append(tests, struct {
			name, username, hash string
			valid                bool
		}{
			fmt.Sprintf("cost %d", cost), cfg.AdminUsername, fmt.Sprintf("$2a$%02d$", cost) + cfg.AdminPasswordHash[7:], cost >= 10 && cost <= 14,
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cfg
			candidate.AdminUsername, candidate.AdminPasswordHash = tt.username, tt.hash
			enabled, origin, err := candidate.AdminConfiguration()
			if tt.valid {
				require.NoError(t, err)
				assert.True(t, enabled)
				assert.Equal(t, "https://example.com", origin)
			} else {
				require.ErrorIs(t, err, ErrAdminConfiguration)
				assert.Equal(t, "invalid admin configuration", err.Error())
				assert.False(t, enabled)
				assert.Empty(t, origin)
			}
		})
	}
}

func TestAdminConfiguration_Origin(t *testing.T) {
	cfg := adminTestConfig(t)
	for _, tt := range []struct{ name, site, origin string }{
		{"https", "https://example.com", "https://example.com"},
		{"path", "https://EXAMPLE.com/app/", "https://example.com"},
		{"default port", "https://example.com:443", "https://example.com"},
		{"custom port", "https://example.com:8443", "https://example.com:8443"},
		{"IPv4", "https://127.0.0.1", "https://127.0.0.1"},
		{"IPv6", "https://[::1]:443", "https://[::1]"},
		{"IPv6 custom port", "https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443"},
		{"empty", "", ""},
		{"http", "http://example.com", ""},
		{"userinfo", "https://user:password@example.com", ""},
		{"query", "https://example.com/?a=b", ""},
		{"empty query", "https://example.com/?", ""},
		{"fragment", "https://example.com/#a", ""},
		{"empty fragment", "https://example.com/#", ""},
		{"opaque", "https:example.com", ""},
		{"missing host", "https:///path", ""},
		{"empty port", "https://example.com:", ""},
		{"zero port", "https://example.com:0", ""},
		{"large port", "https://example.com:65536", ""},
		{"nonnumeric port", "https://example.com:abc", ""},
		{"invalid DNS label", "https://-example.com", ""},
		{"long DNS label", "https://" + strings.Repeat("a", 64) + ".com", ""},
		{"empty DNS label", "https://example..com", ""},
		{"bracketed DNS", "https://[example.com]", ""},
		{"bracketed IPv4", "https://[127.0.0.1]", ""},
		{"unbracketed IPv6", "https://2001:db8::1:443", ""},
		{"IPv6 zone", "https://[fe80::1%25eth0]", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cfg
			candidate.SiteURL = tt.site
			enabled, origin, err := candidate.AdminConfiguration()
			if tt.origin == "" {
				require.ErrorIs(t, err, ErrAdminConfiguration)
				assert.False(t, enabled)
				assert.Empty(t, origin)
			} else {
				require.NoError(t, err)
				assert.True(t, enabled)
				assert.Equal(t, tt.origin, origin)
			}
		})
	}
}

func TestLoad_AdminConfiguration(t *testing.T) {
	cfg := adminTestConfig(t)
	for key, value := range map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:test-only", "TELEGRAM_ADMIN_ID": "123",
		"LOG_LEVEL": "info", "HEARTBEAT_INTERVAL": "60", "HEARTBEAT_URL": "", "SENTRY_DSN": "",
		"WEB_SERVER_PORT": "8080", "TRIAL_DURATION_HOURS": "3", "TRIAL_RATE_LIMIT": "3",
		"GLOBAL_SUB_URL": "https://example.com/sub/", "SITE_URL": cfg.SiteURL,
		"ADMIN_USERNAME": cfg.AdminUsername, "ADMIN_PASSWORD_HASH": cfg.AdminPasswordHash,
	} {
		t.Setenv(key, value)
	}
	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, cfg.AdminUsername, loaded.AdminUsername)
	assert.Equal(t, cfg.AdminPasswordHash, loaded.AdminPasswordHash)
	enabled, origin, err := loaded.AdminConfiguration()
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, cfg.SiteURL, origin)
	assert.NotContains(t, loaded.String(), cfg.AdminUsername)
	assert.NotContains(t, loaded.String(), cfg.AdminPasswordHash)

	t.Setenv("ADMIN_PASSWORD_HASH", "test-only-invalid-hash")
	loaded, err = Load()
	require.ErrorIs(t, err, ErrAdminConfiguration)
	assert.Nil(t, loaded)
	assert.NotContains(t, err.Error(), "test-only-invalid-hash")
}
