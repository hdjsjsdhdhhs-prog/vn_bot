package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func templateReason(t *testing.T, err error) string {
	t.Helper()
	var te *warmupTemplateError
	require.True(t, errors.As(err, &te), "expected a template error, got %v", err)
	return te.Reason
}

func TestValidateTelegramHTML(t *testing.T) {
	t.Parallel()
	valid := []string{
		"plain text",
		"<b>bold</b> <i>it</i> <u>u</u> <s>s</s> <strong>x</strong> <em>y</em> <ins>i</ins> <del>d</del> <strike>z</strike>",
		`<a href="https://example.com/?a=1&amp;b=2">link</a>`,
		`<span class="tg-spoiler">secret</span> <tg-spoiler>s</tg-spoiler>`,
		`<pre><code class="language-go">x := 1</code></pre>`,
		"<blockquote expandable>quote</blockquote>",
		"5 &lt; 6 &amp;&amp; 7 &gt; 3 &quot;q&quot; &#128512; &#x1F600;",
		"«{{plan_name}}» действует до {{expire_date}}.\n\n<b>Привет, {{ username }}</b>",
		"<B>upper-case tag</B>",
	}
	for _, s := range valid {
		assert.NoError(t, validateTelegramHTML(s), s)
	}
	invalid := map[string]string{
		"<b>unclosed":                         "html_unclosed_tag",
		"<b><i>x</b></i>":                     "html_bad_nesting",
		"</b>":                                "html_bad_nesting",
		"<div>x</div>":                        "html_unsupported_tag",
		"<script>alert(1)</script>":           "html_unsupported_tag",
		`<b onclick="x">x</b>`:                "html_bad_attribute",
		`<span class="red">x</span>`:          "html_bad_attribute",
		`<a href="javascript:alert(1)">x</a>`: "html_bad_link",
		`<a href="https://a.b"><a href="https://c.d">x</a></a>`: "html_bad_nesting",
		"a & b":                        "html_unescaped",
		"a > b":                        "html_unescaped",
		"x <b":                         "html_unclosed_tag",
		`<a href="{{username}}">x</a>`: "html_bad_tag",
		`<code class="evil">x</code>`:  "html_bad_attribute",
		"\xff":                         "html_encoding",
	}
	for s, reason := range invalid {
		err := validateTelegramHTML(s)
		require.Error(t, err, s)
		assert.Equal(t, reason, templateReason(t, err), s)
	}
}

func TestWarmupPlaceholdersAndRender(t *testing.T) {
	t.Parallel()
	names, err := warmupPlaceholders("Hi {{username}}, {{ plan_name }} {{username}}")
	require.NoError(t, err)
	assert.Equal(t, []string{"username", "plan_name"}, names)
	for _, bad := range []string{"{{username", "username}}", "{{User}}", "{{ }}", "{x}} {{"} {
		_, err := warmupPlaceholders(bad)
		require.Error(t, err, bad)
		assert.Equal(t, "placeholder_malformed", templateReason(t, err), bad)
	}

	assert.Equal(t, "variable_unknown", templateReason(t, checkWarmupPlaceholders([]string{"first_name"}, WarmupTriggerBotStarted, false)))
	assert.Equal(t, "variable_unavailable", templateReason(t, checkWarmupPlaceholders([]string{"plan_name"}, WarmupTriggerBotStarted, false)))
	assert.Equal(t, "variable_unavailable", templateReason(t, checkWarmupPlaceholders([]string{"expire_date"}, WarmupTriggerTrialActivated, false)))
	assert.Equal(t, "variable_needs_discount", templateReason(t, checkWarmupPlaceholders([]string{"discount_code"}, WarmupTriggerPaidExpired, false)))
	assert.NoError(t, checkWarmupPlaceholders([]string{"discount_code", "plan_name", "expire_date", "username"}, WarmupTriggerPaidExpired, true))

	// Values are HTML-escaped: a username cannot inject markup.
	out, err := renderWarmupTemplate("<b>{{username}}</b> {{ telegram_id }}", map[string]string{"username": "<i>x</i>&\"", "telegram_id": "42"})
	require.NoError(t, err)
	assert.Equal(t, "<b>&lt;i&gt;x&lt;/i&gt;&amp;&#34;</b> 42", out)
	require.NoError(t, validateTelegramHTML(out))
	_, err = renderWarmupTemplate("{{plan_name}}", map[string]string{})
	assert.Equal(t, "variable_unavailable", templateReason(t, err), "a variable without a value is never sent")
}

func TestWarmupVisibleLength(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 5, warmupVisibleLength("<b>hello</b>"))
	assert.Equal(t, 5, warmupVisibleLength("a &lt; b"))
	assert.Equal(t, 2, warmupVisibleLength("😀"), "Telegram counts UTF-16 code units")
	assert.Equal(t, 6, warmupVisibleLength("Привет"))
}

func TestNormalizeWarmupButtons(t *testing.T) {
	t.Parallel()
	ok, err := normalizeWarmupButtons([]WarmupButton{
		{Text: " Тарифы ", Kind: "callback", Value: "buy_premium_list"},
		{Text: "Сайт", Kind: "url", Value: "https://example.com/trial"},
		{Text: "Mini App", Kind: "web_app", Value: "https://example.com/miniapp/"},
		{Text: "Пробник", Kind: "url", Value: ""},
	})
	require.NoError(t, err)
	assert.Equal(t, "Тарифы", ok[0].Text)
	visible, hidden := visibleWarmupButtons(ok)
	assert.Len(t, visible, 3)
	assert.Equal(t, 1, hidden, "a URL button without a URL is never sent")

	cases := map[string]WarmupButton{
		"button_callback": {Text: "x", Kind: "callback", Value: "admin_stats"},
		"button_kind":     {Text: "x", Kind: "switch_inline", Value: "q"},
		"button_text":     {Text: " ", Kind: "callback", Value: "menu_help"},
		"button_url":      {Text: "x", Kind: "url", Value: "javascript:alert(1)"},
	}
	for reason, b := range cases {
		_, err := normalizeWarmupButtons([]WarmupButton{b})
		assert.Equal(t, reason, templateReason(t, err), reason)
	}
	_, err = normalizeWarmupButtons([]WarmupButton{{Text: "x", Kind: "web_app", Value: "http://example.com"}})
	assert.Equal(t, "button_url", templateReason(t, err), "Mini App needs https")
	_, err = normalizeWarmupButtons([]WarmupButton{{Text: "{{username}}", Kind: "callback", Value: "menu_help"}})
	assert.Equal(t, "button_text", templateReason(t, err))
	many := make([]WarmupButton, warmupMaxButtons+1)
	for i := range many {
		many[i] = WarmupButton{Text: "x", Kind: "callback", Value: "menu_help"}
	}
	_, err = normalizeWarmupButtons(many)
	assert.Equal(t, "buttons_too_many", templateReason(t, err))
}

func TestNormalizeWarmupButtons_SameRow(t *testing.T) {
	t.Parallel()
	rows, err := normalizeWarmupButtons([]WarmupButton{
		{Text: "Первая", Kind: "callback", Value: "menu_help", SameRow: true},
		{Text: "Вторая", Kind: "callback", Value: "menu_support", SameRow: true},
		{Text: "Третья", Kind: "callback", Value: "back_to_start"},
	})
	require.NoError(t, err)
	assert.False(t, rows[0].SameRow, "the first button always starts the keyboard")
	assert.True(t, rows[1].SameRow)
	assert.False(t, rows[2].SameRow)

	// Buttons stored before rows existed decode as one button per row.
	old, err := DecodeWarmupButtons(`[{"text":"x","kind":"callback","value":"menu_help"}]`)
	require.NoError(t, err)
	assert.False(t, old[0].SameRow)
	stored, err := DecodeWarmupButtons(`[{"text":"x","kind":"callback","value":"menu_help","same_row":true}]`)
	require.NoError(t, err)
	assert.True(t, stored[0].SameRow)
	encoded, err := encodeWarmupJSON([]WarmupButton{{Text: "x", Kind: "callback", Value: "menu_help"}})
	require.NoError(t, err)
	assert.JSONEq(t, `[{"text":"x","kind":"callback","value":"menu_help"}]`, encoded, "same_row is omitted when false")
}

func TestWarmupConditionsDecodeAndMatch(t *testing.T) {
	t.Parallel()
	c, err := DecodeWarmupConditions(`{"had_trial":false,"languages":["ru"]}`)
	require.NoError(t, err)
	require.NotNil(t, c.HadTrial)
	assert.False(t, *c.HadTrial)
	_, err = DecodeWarmupConditions(`{"country":"RU"}`)
	assert.Equal(t, "conditions_json", templateReason(t, err), "unknown condition keys are rejected")
	_, err = DecodeWarmupButtons(`[{"text":"x","kind":"url","value":"","extra":1}]`)
	assert.Equal(t, "buttons_json", templateReason(t, err))

	norm, err := normalizeWarmupConditions(WarmupConditions{Languages: []string{" RU ", "ru", "uk"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"ru", "uk"}, norm.Languages)
	_, err = normalizeWarmupConditions(WarmupConditions{Languages: []string{"russian"}})
	assert.Equal(t, "conditions_languages", templateReason(t, err))

	assert.True(t, matchWarmupLanguage(nil, ""))
	assert.True(t, matchWarmupLanguage([]string{"ru"}, "ru-RU"))
	assert.False(t, matchWarmupLanguage([]string{"ru"}, "en"))
	assert.False(t, matchWarmupLanguage([]string{"ru"}, ""), "an unknown language never matches a language filter")
}

func TestWarmupTrialDurationAndPlural(t *testing.T) {
	t.Parallel()
	days, words := warmupTrialDuration(72)
	assert.Equal(t, "3", days)
	assert.Equal(t, "3 дня", words)
	days, words = warmupTrialDuration(24)
	assert.Equal(t, "1", days)
	assert.Equal(t, "1 день", words)
	days, words = warmupTrialDuration(36)
	assert.Equal(t, "2", days)
	assert.Equal(t, "36 часов", words)
	_, words = warmupTrialDuration(168)
	assert.Equal(t, "7 дней", words)
	_, words = warmupTrialDuration(21)
	assert.Equal(t, "21 час", words)
	assert.Equal(t, "дней", russianPlural(11, "день", "дня", "дней"))
	assert.Equal(t, "дня", russianPlural(22, "день", "дня", "дней"))
}
