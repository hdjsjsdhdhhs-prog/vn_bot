package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Telegram HTML (parse_mode=HTML) validation, {{variable}} templates and the
// JSON documents of a scenario (buttons, conditions). MarkdownV2 is never
// used by the warm-up.

// Telegram limits.
const (
	warmupMaxTextLength    = 4096 // sendMessage text, after entity parsing
	warmupMaxCaptionLength = 1024 // sendPhoto caption, after entity parsing
	warmupMaxButtons       = 8
	warmupMaxButtonText    = 64
	warmupMaxButtonURL     = 2048
	warmupMaxLanguages     = 20
)

// warmupTemplateError is a stable reason code of an invalid message.
type warmupTemplateError struct {
	Reason string
	Detail string
}

func (e *warmupTemplateError) Error() string {
	if e.Detail == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Detail
}

func templateErr(reason, detail string) error {
	return &warmupTemplateError{Reason: reason, Detail: detail}
}

// warmupAllowedTags are the Telegram HTML tags with their allowed attributes.
var warmupAllowedTags = map[string]map[string]bool{
	"b": {}, "strong": {}, "i": {}, "em": {}, "u": {}, "ins": {}, "s": {}, "strike": {}, "del": {},
	"tg-spoiler": {}, "span": {"class": true}, "a": {"href": true}, "code": {"class": true}, "pre": {},
	"blockquote": {"expandable": true},
}

var (
	warmupTagName     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	warmupAttrPattern = regexp.MustCompile(`^\s*([a-z][a-z-]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'))?`)
	warmupEntity      = regexp.MustCompile(`^&(?:lt|gt|amp|quot|#[0-9]{1,7}|#x[0-9a-fA-F]{1,6});`)
	warmupCodeClass   = regexp.MustCompile(`^language-[A-Za-z0-9_+#-]{1,32}$`)
	// warmupPlaceholder matches one {{variable}}; spaces inside are allowed.
	warmupPlaceholder = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)
)

// validateTelegramHTML checks that s is accepted by Telegram parse_mode=HTML:
// only supported tags and attributes, properly nested and closed, and every
// <, > and & outside a tag written as an entity. Placeholders are text, but
// never inside a tag (a value would not be escaped for an attribute).
func validateTelegramHTML(s string) error {
	if !utf8.ValidString(s) {
		return templateErr("html_encoding", "")
	}
	var stack []string
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return templateErr("html_unclosed_tag", "")
			}
			raw := s[i+1 : i+end]
			i += end + 1
			if strings.Contains(raw, "{{") || strings.Contains(raw, "<") {
				return templateErr("html_bad_tag", raw)
			}
			if strings.HasPrefix(raw, "/") {
				name := strings.ToLower(strings.TrimSpace(raw[1:]))
				if len(stack) == 0 || stack[len(stack)-1] != name {
					return templateErr("html_bad_nesting", name)
				}
				stack = stack[:len(stack)-1]
				continue
			}
			name, attrs, err := parseWarmupTag(raw)
			if err != nil {
				return err
			}
			err = checkWarmupTag(name, attrs, stack)
			if err != nil {
				return err
			}
			stack = append(stack, name)
		case '>':
			return templateErr("html_unescaped", ">")
		case '&':
			m := warmupEntity.FindString(s[i:])
			if m == "" {
				return templateErr("html_unescaped", "&")
			}
			i += len(m)
		default:
			i++
		}
	}
	if len(stack) > 0 {
		return templateErr("html_unclosed_tag", stack[len(stack)-1])
	}
	return nil
}

func parseWarmupTag(raw string) (string, map[string]string, error) {
	raw = strings.TrimSpace(raw)
	nameEnd := strings.IndexFunc(raw, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	name, rest := raw, ""
	if nameEnd >= 0 {
		name, rest = raw[:nameEnd], raw[nameEnd:]
	}
	name = strings.ToLower(name)
	if !warmupTagName.MatchString(name) {
		return "", nil, templateErr("html_bad_tag", raw)
	}
	attrs := map[string]string{}
	for strings.TrimSpace(rest) != "" {
		m := warmupAttrPattern.FindStringSubmatch(rest)
		if m == nil {
			return "", nil, templateErr("html_bad_attribute", name)
		}
		value := m[2]
		if value == "" {
			value = m[3]
		}
		if _, dup := attrs[m[1]]; dup {
			return "", nil, templateErr("html_bad_attribute", name+" "+m[1])
		}
		attrs[m[1]] = html.UnescapeString(value)
		rest = rest[len(m[0]):]
	}
	return name, attrs, nil
}

func checkWarmupTag(name string, attrs map[string]string, stack []string) error {
	allowed, ok := warmupAllowedTags[name]
	if !ok {
		return templateErr("html_unsupported_tag", name)
	}
	for attr := range attrs {
		if !allowed[attr] {
			return templateErr("html_bad_attribute", name+" "+attr)
		}
	}
	switch name {
	case "a":
		err := validateWarmupURL(attrs["href"], false)
		if err != nil {
			return templateErr("html_bad_link", attrs["href"])
		}
		if slices.Contains(stack, "a") {
			return templateErr("html_bad_nesting", "a")
		}
	case "span":
		if attrs["class"] != "tg-spoiler" {
			return templateErr("html_bad_attribute", "span class")
		}
	case "code":
		if class, has := attrs["class"]; has && !warmupCodeClass.MatchString(class) {
			return templateErr("html_bad_attribute", "code class")
		}
	}
	return nil
}

// warmupVisibleLength is the length Telegram counts against its limits: the
// text without tags, entities decoded, in UTF-16 code units.
func warmupVisibleLength(s string) int {
	var plain strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '<' {
			if end := strings.IndexByte(s[i:], '>'); end >= 0 {
				i += end + 1
				continue
			}
		}
		plain.WriteByte(s[i])
		i++
	}
	n := 0
	for _, r := range html.UnescapeString(plain.String()) {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// warmupPlaceholders returns the variable names used by s (in order, unique)
// and rejects stray braces that are not a well-formed placeholder.
func warmupPlaceholders(s string) ([]string, error) {
	seen := map[string]bool{}
	names := []string{}
	for _, m := range warmupPlaceholder.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	rest := warmupPlaceholder.ReplaceAllString(s, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return nil, templateErr("placeholder_malformed", "")
	}
	return names, nil
}

// checkWarmupPlaceholders rejects unknown variables and variables without a
// value for the trigger (or a discount variable without a discount).
func checkWarmupPlaceholders(names []string, trigger string, discount bool) error {
	for _, name := range names {
		v, ok := warmupVariableByName(name)
		if !ok {
			return templateErr("variable_unknown", name)
		}
		if !v.availableFor(trigger) {
			return templateErr("variable_unavailable", name)
		}
		if v.Discount && !discount {
			return templateErr("variable_needs_discount", name)
		}
	}
	return nil
}

// renderWarmupTemplate substitutes every placeholder with its HTML-escaped
// value. A placeholder without a value fails: a message is never sent with
// an unknown or unavailable variable.
func renderWarmupTemplate(s string, values map[string]string) (string, error) {
	var missing string
	out := warmupPlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		name := warmupPlaceholder.FindStringSubmatch(m)[1]
		value, ok := values[name]
		if !ok {
			if missing == "" {
				missing = name
			}
			return m
		}
		return html.EscapeString(value)
	})
	if missing != "" {
		return "", templateErr("variable_unavailable", missing)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Buttons
// ---------------------------------------------------------------------------

// WarmupButton is one inline keyboard button. A button starts a new keyboard
// row unless SameRow puts it next to the previous one (absent in buttons
// stored before rows existed: one button per row, as before).
type WarmupButton struct {
	Text    string `json:"text"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	SameRow bool   `json:"same_row,omitempty"`
}

func validateWarmupURL(raw string, httpsOnly bool) error {
	if raw == "" || len(raw) > warmupMaxButtonURL || strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("bad url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("bad url")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && !httpsOnly:
	case u.Scheme == "tg" && !httpsOnly:
	default:
		return errors.New("bad scheme")
	}
	return nil
}

// normalizeWarmupButtons trims and validates buttons. A URL or Mini App button
// without a URL is kept (the admin may fill it in later) but is never sent:
// visibleWarmupButtons drops it and the preview warns.
func normalizeWarmupButtons(in []WarmupButton) ([]WarmupButton, error) {
	if len(in) > warmupMaxButtons {
		return nil, templateErr("buttons_too_many", "")
	}
	out := make([]WarmupButton, 0, len(in))
	for i, b := range in {
		b.Text, b.Kind, b.Value = strings.TrimSpace(b.Text), strings.TrimSpace(b.Kind), strings.TrimSpace(b.Value)
		if i == 0 {
			b.SameRow = false // the first button always starts the keyboard
		}
		field := fmt.Sprintf("buttons[%d]", i)
		if b.Text == "" || utf8.RuneCountInString(b.Text) > warmupMaxButtonText {
			return nil, templateErr("button_text", field)
		}
		if strings.Contains(b.Text, "{{") {
			return nil, templateErr("button_text", field)
		}
		switch b.Kind {
		case WarmupButtonURL, WarmupButtonWebApp:
			if b.Value != "" {
				err := validateWarmupURL(b.Value, b.Kind == WarmupButtonWebApp)
				if err != nil {
					return nil, templateErr("button_url", field)
				}
			}
		case WarmupButtonCallback:
			if !isWarmupCallbackAllowed(b.Value) {
				return nil, templateErr("button_callback", field)
			}
		default:
			return nil, templateErr("button_kind", field)
		}
		out = append(out, b)
	}
	return out, nil
}

// visibleWarmupButtons drops URL/Mini App buttons without a URL.
func visibleWarmupButtons(buttons []WarmupButton) (visible []WarmupButton, hidden int) {
	visible = []WarmupButton{}
	for _, b := range buttons {
		if (b.Kind == WarmupButtonURL || b.Kind == WarmupButtonWebApp) && b.Value == "" {
			hidden++
			continue
		}
		visible = append(visible, b)
	}
	return visible, hidden
}

// DecodeWarmupButtons parses the stored buttons JSON.
func DecodeWarmupButtons(raw string) ([]WarmupButton, error) {
	buttons := []WarmupButton{}
	if strings.TrimSpace(raw) == "" {
		return buttons, nil
	}
	err := decodeStrictJSON(raw, &buttons)
	if err != nil {
		return nil, templateErr("buttons_json", err.Error())
	}
	return buttons, nil
}

func encodeWarmupJSON(v any) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeStrictJSON(raw string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Conditions
// ---------------------------------------------------------------------------

// WarmupConditions restricts who receives a scenario. A nil flag is "any";
// Languages matches the language code prefix ("ru" matches "ru-RU"); an
// empty list is any language. Unknown keys are rejected.
type WarmupConditions struct {
	HadTrial        *bool    `json:"had_trial,omitempty"`
	HasSubscription *bool    `json:"has_subscription,omitempty"`
	HasActivePaid   *bool    `json:"has_active_paid,omitempty"`
	EverPaid        *bool    `json:"ever_paid,omitempty"`
	Connected       *bool    `json:"connected,omitempty"`
	Languages       []string `json:"languages,omitempty"`
}

var warmupLanguagePattern = regexp.MustCompile(`^[a-z]{2,3}$`)

// DecodeWarmupConditions parses (strictly) the stored conditions JSON.
func DecodeWarmupConditions(raw string) (WarmupConditions, error) {
	var c WarmupConditions
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	err := decodeStrictJSON(raw, &c)
	if err != nil {
		return c, templateErr("conditions_json", err.Error())
	}
	return c, nil
}

func normalizeWarmupConditions(c WarmupConditions) (WarmupConditions, error) {
	if len(c.Languages) > warmupMaxLanguages {
		return c, templateErr("conditions_languages", "")
	}
	seen := map[string]bool{}
	langs := []string{}
	for _, l := range c.Languages {
		l = strings.ToLower(strings.TrimSpace(l))
		if !warmupLanguagePattern.MatchString(l) {
			return c, templateErr("conditions_languages", l)
		}
		if !seen[l] {
			seen[l] = true
			langs = append(langs, l)
		}
	}
	c.Languages = nil
	if len(langs) > 0 {
		c.Languages = langs
	}
	return c, nil
}

func matchWarmupFlag(want *bool, have bool) bool {
	return want == nil || *want == have
}

func matchWarmupLanguage(langs []string, code string) bool {
	if len(langs) == 0 {
		return true
	}
	code = strings.ToLower(code)
	if i := strings.IndexAny(code, "-_"); i >= 0 {
		code = code[:i]
	}
	return slices.Contains(langs, code)
}
