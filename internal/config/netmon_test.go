package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func netMonBaseEnv(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456789:ABCdefGHIjklMNOpqrsTUVwxyz")
	t.Setenv("TELEGRAM_ADMIN_ID", "123456")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("GLOBAL_SUB_URL", "https://vpn.example.com/sub/")
}

func TestLoad_NetMonDefaults(t *testing.T) {
	netMonBaseEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.NetMonEnabled)
	assert.Equal(t, DefaultNetMonInterval, cfg.NetMonInterval)
	assert.Equal(t, DefaultNetMonTimeout, cfg.NetMonTimeout)
	assert.Equal(t, DefaultNetMonRefresh, cfg.NetMonRefresh)
	assert.Equal(t, DefaultNetMonConcurrency, cfg.NetMonConcurrency)
	assert.Equal(t, DefaultNetMonDownAfter, cfg.NetMonDownAfter)
}

func TestLoad_NetMonCustom(t *testing.T) {
	netMonBaseEnv(t)
	t.Setenv("NETMON_ENABLED", "false")
	t.Setenv("NETMON_INTERVAL", "120")
	t.Setenv("NETMON_TIMEOUT", "10")
	t.Setenv("NETMON_REFRESH", "900")
	t.Setenv("NETMON_CONCURRENCY", "4")
	t.Setenv("NETMON_DOWN_AFTER", "3")
	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.NetMonEnabled)
	assert.Equal(t, 120, cfg.NetMonInterval)
	assert.Equal(t, 10, cfg.NetMonTimeout)
	assert.Equal(t, 900, cfg.NetMonRefresh)
	assert.Equal(t, 4, cfg.NetMonConcurrency)
	assert.Equal(t, 3, cfg.NetMonDownAfter)
}

func TestLoad_NetMonInvalid(t *testing.T) {
	cases := map[string][2]string{
		"interval too short":    {"NETMON_INTERVAL", "5"},
		"timeout too long":      {"NETMON_TIMEOUT", "31"},
		"timeout negative":      {"NETMON_TIMEOUT", "-1"},
		"refresh too short":     {"NETMON_REFRESH", "30"},
		"concurrency too high":  {"NETMON_CONCURRENCY", "65"},
		"down after too high":   {"NETMON_DOWN_AFTER", "11"},
		"interval not a number": {"NETMON_INTERVAL", "fast"},
		"enabled not a bool":    {"NETMON_ENABLED", "maybe"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			netMonBaseEnv(t)
			t.Setenv(kv[0], kv[1])
			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), kv[0])
		})
	}

	t.Run("timeout must be shorter than interval", func(t *testing.T) {
		netMonBaseEnv(t)
		t.Setenv("NETMON_INTERVAL", "20")
		t.Setenv("NETMON_TIMEOUT", "20")
		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NETMON_TIMEOUT")
	})
}
