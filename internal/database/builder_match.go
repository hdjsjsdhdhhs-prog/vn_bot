package database

import (
	"net/url"
	"strings"
)

// Shared builder matching rules. Both the admin Preview (catalogue based) and
// the /sub runtime (live source based) MUST use these helpers so a rule never
// selects a node in one path and not in the other.

// CountryFromName extracts an ISO country code from the first flag emoji
// (pair of regional indicator symbols) in a server name.
func CountryFromName(name string) string {
	runes := []rune(name)
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if isRegionalIndicator(a) && isRegionalIndicator(b) {
			return string([]rune{'A' + (a - 0x1F1E6), 'A' + (b - 0x1F1E6)})
		}
	}

	return ""
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}

// EntryCountry returns the effective country of an entry: the stored
// catalogue country when set, otherwise the flag emoji in its name.
func EntryCountry(catalogueCountry, name string) string {
	if catalogueCountry != "" {
		return catalogueCountry
	}

	return CountryFromName(name)
}

// CountryMatches reports whether an entry belongs to a country rule.
func CountryMatches(catalogueCountry, name, ruleCountry string) bool {
	country := EntryCountry(catalogueCountry, name)

	return country != "" && strings.EqualFold(country, ruleCountry)
}

// originalNameTiers are applied in order; the first tier producing at least
// one match wins. Exact comparison always has priority, so a literal "+" in a
// name is only treated as an encoded space when nothing matches literally.
var originalNameTiers = []func(string) string{
	func(s string) string { return s },
	func(s string) string { return strings.TrimSpace(unescapeOr(s, url.PathUnescape)) },
	func(s string) string { return strings.TrimSpace(unescapeOr(s, url.QueryUnescape)) },
}

func unescapeOr(s string, fn func(string) (string, error)) string {
	if out, err := fn(s); err == nil {
		return out
	}

	return s
}

// MatchOriginalName returns the indexes of names matching want for the
// original_name fallback. Comparison is tiered: exact, then URL path decoding
// (%XX), then query decoding ("+" as space). An empty want matches nothing.
func MatchOriginalName(want string, names []string) []int {
	if want == "" {
		return nil
	}

	for _, norm := range originalNameTiers {
		target := norm(want)
		if target == "" {
			continue
		}

		var out []int
		for i, n := range names {
			if norm(n) == target {
				out = append(out, i)
			}
		}

		if len(out) > 0 {
			return out
		}
	}

	return nil
}
