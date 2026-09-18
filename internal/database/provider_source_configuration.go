package database

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// ErrProviderSourceUnusable never includes upstream URLs or credentials.
var ErrProviderSourceUnusable = errors.New("provider source is disabled or unusable")

// RequestConfiguration is shared by assignment and runtime fetching. It checks
// local configuration only, not upstream availability, and returns normalized
// headers and values that must never be reflected in a customer response.
func (s ProviderSource) RequestConfiguration() (map[string]string, []string, error) {
	if !s.Enabled || strings.TrimSpace(s.SubscriptionURL) == "" {
		return nil, nil, ErrProviderSourceUnusable
	}
	u, err := url.Parse(s.SubscriptionURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Fragment != "" {
		return nil, nil, ErrProviderSourceUnusable
	}
	if !validProviderHeaderValue(s.HWID) || !validProviderHeaderValue(s.UserAgent) {
		return nil, nil, ErrProviderSourceUnusable
	}

	headers := make(map[string]string)
	if raw := strings.TrimSpace(s.Headers); raw != "" {
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			return nil, nil, ErrProviderSourceUnusable
		}
	}
	normalized := make(map[string]string, len(headers))
	sensitive := []string{s.SubscriptionURL, s.HWID, s.UserAgent}
	for key, value := range headers {
		key = strings.ToLower(strings.TrimSpace(key))
		if !validProviderHeaderName(key) || !validProviderHeaderValue(value) {
			return nil, nil, ErrProviderSourceUnusable
		}
		if _, duplicate := normalized[key]; duplicate {
			return nil, nil, ErrProviderSourceUnusable
		}
		normalized[key] = value
		sensitive = append(sensitive, value)
	}
	return normalized, sensitive, nil
}

func validProviderHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func validProviderHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if (value[i] < 32 && value[i] != '\t') || value[i] == 127 {
			return false
		}
	}
	return true
}
