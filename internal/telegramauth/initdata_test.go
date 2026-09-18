package telegramauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBotToken = "123456:TEST_ONLY_TOKEN"

// Independent Python stdlib HMAC vector, including signature, encoded special
// characters, a >32-bit ID and prefix keys (sort keys, not key=value lines).
const goldenInitData = "auth_date=1800000000&query_id=AAE%2Btest%2F&signature=third-party-signature&user=%7B%22id%22%3A1234567890123%2C%22first_name%22%3A%22Alice+%2B+%26+%2F+%3D%22%2C%22photo_url%22%3A%22https%3A%2F%2Fexample.test%2Fphoto%22%7D&z=last&z0=sort-keys-not-lines&hash=b2be76f7d851ee1e59517359304a8517c769f4047d87485b079c5c395dc44521"

func signFields(fields url.Values, token string) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var pairs []string
	for _, key := range keys {
		pairs = append(pairs, key+"="+fields.Get(key))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	_, _ = mac.Write([]byte(strings.Join(pairs, "\n")))
	fields.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return fields.Encode()
}

func TestVerifyGolden(t *testing.T) {
	validator := New(testBotToken)
	now := time.Unix(1800000000, 0)
	id, err := validator.Verify(goldenInitData, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1234567890123), id)
	// Decoding once preserves values, irrespective of ordering/space encoding.
	fields, err := url.ParseQuery(goldenInitData)
	require.NoError(t, err)
	id, err = validator.Verify(strings.ReplaceAll(fields.Encode(), "+", "%20"), now)
	require.NoError(t, err)
	assert.Equal(t, int64(1234567890123), id)
	fields.Set("signature", "tampered")
	_, err = validator.Verify(fields.Encode(), now)
	require.ErrorIs(t, err, ErrInvalidInitData)
}

func TestVerifyRejectsUntrustedData(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for name, raw := range map[string]string{
		"missing":              "",
		"malformed escape":     "%zz",
		"oversized":            strings.Repeat("a", MaxInitDataBytes+1),
		"tampered identity":    strings.Replace(goldenInitData, "1234567890123", "1234567890124", 1),
		"tampered date":        strings.Replace(goldenInitData, "1800000000", "1800000001", 1),
		"unsigned extra field": goldenInitData + "&admin=true",
		"duplicate date":       goldenInitData + "&auth_date=1800000000",
		"encoded duplicate":    goldenInitData + "&%75ser=%7B%22id%22%3A1%7D",
		"duplicate hash":       goldenInitData + "&hash=" + strings.Repeat("a", 64),
		"bare field":           goldenInitData + "&bare",
		"empty component":      goldenInitData + "&",
		"semicolon":            goldenInitData + ";x=y",
		"double encoded":       url.QueryEscape(goldenInitData),
		"bad hash hex":         strings.Replace(goldenInitData, "b2be76f7", "zzzzzzzz", 1),
		"short hash":           goldenInitData[:len(goldenInitData)-2],
	} {
		t.Run(name, func(t *testing.T) {
			id, err := New(testBotToken).Verify(raw, now)
			require.ErrorIs(t, err, ErrInvalidInitData)
			assert.Zero(t, id)
			assert.Equal(t, "invalid Telegram authentication", err.Error())
		})
	}
	_, err := New("another:BOT").Verify(goldenInitData, now)
	require.ErrorIs(t, err, ErrInvalidInitData)
	for _, validator := range []*Validator{nil, {}, New(""), New(" \t")} {
		id, err := validator.Verify(goldenInitData, now)
		require.ErrorIs(t, err, ErrNotConfigured)
		assert.Zero(t, id)
	}
}

func TestVerifySignedMalformedFields(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for name, mutate := range map[string]func(url.Values){
		"missing user":  func(v url.Values) { v.Del("user") },
		"missing date":  func(v url.Values) { v.Del("auth_date") },
		"empty key":     func(v url.Values) { v.Set("", "x") },
		"newline key":   func(v url.Values) { v.Set("x\ny", "z") },
		"newline value": func(v url.Values) { v.Set("query_id", "x\ny=z") },
		"equals key":    func(v url.Values) { v.Set("x=y", "z") },
		"nul value":     func(v url.Values) { v.Set("query_id", "x\x00") },
	} {
		t.Run(name, func(t *testing.T) {
			fields := url.Values{"user": {`{"id":42}`}, "auth_date": {"1800000000"}, "query_id": {"test"}}
			mutate(fields)
			_, err := New(testBotToken).Verify(signFields(fields, testBotToken), now)
			require.ErrorIs(t, err, ErrInvalidInitData)
		})
	}
	for _, user := range []string{
		"", "null", "[]", "42", `{"id":0}`, `{"id":-1}`, `{"id":"42"}`, `{"id":1.5}`, `{"id":1e3}`,
		`{"id":4503599627370496}`, `{"id":9223372036854775808}`, `{"id":null}`, `{"ID":42}`,
		`{"id":42,"id":43}`, `{"id":42,"\u0069d":43}`, `{"id":42} {"id":43}`,
		`{"id":42,"is_bot":true}`, `{"id":42,"is_bot":null}`, `{"id":42,"is_bot":"false"}`, `{"receiver":{"id":42}}`,
	} {
		t.Run("user="+user, func(t *testing.T) {
			fields := url.Values{"user": {user}, "auth_date": {"1800000000"}}
			id, err := New(testBotToken).Verify(signFields(fields, testBotToken), now)
			require.ErrorIs(t, err, ErrInvalidInitData)
			assert.Zero(t, id)
		})
	}
}

func TestVerifyIdentityComesOnlyFromUser(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, id := range []int64{1, (1 << 52) - 1} {
		fields := url.Values{
			"auth_date": {"1800000000"},
			"user":      {`{"id":` + strconv.FormatInt(id, 10) + `,"username":"another-user"}`},
			"receiver":  {`{"id":999}`},
			"chat":      {`{"id":-999}`},
		}
		got, err := New(testBotToken).Verify(signFields(fields, testBotToken), now)
		require.NoError(t, err)
		assert.Equal(t, id, got)
		fields.Del("user")
		_, err = New(testBotToken).Verify(signFields(fields, testBotToken), now)
		require.ErrorIs(t, err, ErrInvalidInitData, "chat and receiver cannot supply user identity")
	}
}

func TestVerifyFreshness(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, offset := range []int64{-301, -300, -299, 0, 30, 31} {
		fields := url.Values{"user": {`{"id":42,"is_bot":false}`}, "auth_date": {strconv.FormatInt(now.Unix()+offset, 10)}}
		id, err := New(testBotToken).Verify(signFields(fields, testBotToken), now)
		if offset < -300 || offset > 30 {
			require.ErrorIs(t, err, ErrInvalidInitData)
			assert.Zero(t, id)
		} else {
			require.NoError(t, err)
			assert.Equal(t, int64(42), id)
		}
	}
	for _, date := range []string{"", "abc", "0", "-1", "+1800000000", "01800000000", "1800000000.0", "9223372036854775807", "-9223372036854775808", "9223372036854775808"} {
		fields := url.Values{"user": {`{"id":42}`}, "auth_date": {date}}
		_, err := New(testBotToken).Verify(signFields(fields, testBotToken), now)
		require.ErrorIs(t, err, ErrInvalidInitData, date)
	}
}

func FuzzVerify(f *testing.F) {
	f.Add(goldenInitData)
	f.Add("%zz")
	f.Add("user=null&auth_date=0&hash=0")
	validator := New(testBotToken)
	f.Fuzz(func(t *testing.T, raw string) {
		id, err := validator.Verify(raw, time.Unix(1800000000, 0))
		if err != nil {
			require.ErrorIs(t, err, ErrInvalidInitData)
			assert.Zero(t, id)
		} else {
			assert.Positive(t, id)
		}
	})
}
