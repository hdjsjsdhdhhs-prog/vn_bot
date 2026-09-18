package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// fetchHTTPClient is a shared HTTP client for fetching subscription responses
// from upstream nodes (3x-ui JSON, Clash YAML, base64, plain links). It limits
// idle connections (2 per host) and keeps transparent gzip decompression
// enabled (DisableCompression is false), so resp.Body is the decompressed
// content that DetectFormat and the parsers consume directly.
var fetchHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	},
}

const defaultSourceUserAgent = "RS8 KVN Subserver"

// NodeResponse holds the body and headers returned by an upstream node's
// subscription endpoint (3x-ui JSON, Clash YAML, base64, plain links).
type NodeResponse struct {
	Body    []byte
	Headers map[string]string
}

// FetchFromNode sends an HTTP GET to url with a custom User-Agent and returns
// the response body (up to config.MaxResponseSize) together with all response headers stored
// under lowercased keys. Header values are taken from the first value for each key.
// Non-2xx responses are treated as fetch errors so upstream failures are never
// aggregated into a subscription.
func FetchFromNode(ctx context.Context, url string) (*NodeResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error("Failed to create HTTP request for source fetch",
			zap.String("url", url),
			zap.Error(err))

		return nil, fmt.Errorf("create source fetch request: %w", err)
	}

	req.Header.Set("User-Agent", defaultSourceUserAgent)

	resp, err := fetchHTTPClient.Do(req)
	if err != nil {
		logger.Error("Source fetch request failed",
			zap.String("url", url),
			zap.Error(err))

		return nil, fmt.Errorf("execute source fetch request: %w", err)
	}

	if resp == nil || resp.Body == nil {
		logger.Error("Source fetch returned no response body",
			zap.String("url", url))

		return nil, fmt.Errorf("source fetch returned no body: %s", url)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		logger.Error("Source fetch returned non-2xx status",
			zap.String("url", url),
			zap.Int("status", resp.StatusCode))

		return nil, fmt.Errorf("source fetch %s returned status %d", url, resp.StatusCode)
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logger.Error("Failed to close source response body",
				zap.String("url", url),
				zap.Error(closeErr))
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize+1))
	if err != nil {
		logger.Error("Failed to read source response body",
			zap.String("url", url),
			zap.Error(err))

		return nil, fmt.Errorf("read source response body: %w", err)
	}

	if len(body) > config.MaxResponseSize {
		logger.Error("Source response body exceeds size limit",
			zap.String("url", url),
			zap.Int("limit", config.MaxResponseSize))

		return nil, fmt.Errorf("source response body exceeds %d bytes", config.MaxResponseSize)
	}

	headers := make(map[string]string)

	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}

	return &NodeResponse{
		Body:    body,
		Headers: headers,
	}, nil
}

// FetchFromProviderSource fetches a read-only upstream subscription using only
// server-side ProviderSource credentials. All failures collapse to a safe
// sentinel so URLs and credential-bearing request details cannot escape into
// client-visible errors or application logs.
func FetchFromProviderSource(ctx context.Context, source database.ProviderSource) (*NodeResponse, error) {
	requestHeaders, sensitiveValues, err := validateProviderSourceConfiguration(source)
	if err != nil {
		logger.Warn("Provider source configuration is unusable",
			zap.Uint("provider_source_id", source.ID))

		return nil, ErrProviderSourceUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.SubscriptionURL, nil)
	if err != nil {
		logger.Warn("Failed to create provider source request",
			zap.Uint("provider_source_id", source.ID))

		return nil, ErrProviderSourceUnavailable
	}

	for key, value := range requestHeaders {
		req.Header.Set(key, value)
	}

	if source.UserAgent != "" {
		req.Header.Set("User-Agent", source.UserAgent)
	} else if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultSourceUserAgent)
	}

	if source.HWID != "" {
		req.Header.Set("X-HWID", source.HWID)
	}

	client := providerSourceHTTPClient(requestHeaders, source)
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("Provider source request failed",
			zap.Uint("provider_source_id", source.ID))

		return nil, ErrProviderSourceUnavailable
	}

	if resp == nil || resp.Body == nil {
		logger.Warn("Provider source returned no response body",
			zap.Uint("provider_source_id", source.ID))

		return nil, ErrProviderSourceUnavailable
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Warn("Failed to close provider source response body",
				zap.Uint("provider_source_id", source.ID))
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logger.Warn("Provider source returned non-2xx status",
			zap.Uint("provider_source_id", source.ID),
			zap.Int("status", resp.StatusCode))

		return nil, ErrProviderSourceUnavailable
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize+1))
	if err != nil {
		logger.Warn("Failed to read provider source response",
			zap.Uint("provider_source_id", source.ID))

		return nil, ErrProviderSourceUnavailable
	}

	if len(body) > config.MaxResponseSize {
		logger.Warn("Provider source response exceeds size limit",
			zap.Uint("provider_source_id", source.ID),
			zap.Int("limit", config.MaxResponseSize))

		return nil, ErrProviderSourceUnavailable
	}

	if containsSensitiveProviderValue(string(body), sensitiveValues) {
		logger.Warn("Provider source response contained server-side credentials",
			zap.Uint("provider_source_id", source.ID))

		return nil, ErrProviderSourceUnavailable
	}

	responseHeaders := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) == 0 {
			continue
		}

		lowerKey := strings.ToLower(key)
		if _, configured := requestHeaders[lowerKey]; configured ||
			lowerKey == "user-agent" || lowerKey == "x-hwid" ||
			containsSensitiveProviderValue(values[0], sensitiveValues) {
			continue
		}

		// URL-valued headers can reflect the private endpoint as a relative
		// reference. Do not resolve ordinary metadata against the source URL:
		// a root endpoint would make every such value look like a private URL.
		if strings.HasSuffix(lowerKey, "-url") || lowerKey == "location" || lowerKey == "content-location" {
			if reference, parseErr := url.Parse(values[0]); parseErr == nil &&
				containsSensitiveProviderValue(req.URL.ResolveReference(reference).String(), sensitiveValues) {
				continue
			}
		}

		responseHeaders[lowerKey] = values[0]
	}

	return &NodeResponse{Body: body, Headers: responseHeaders}, nil
}

// validateProviderSourceConfiguration validates fields required by the runtime
// fetch and returns lower-cased configured headers plus values that must never
// appear in a downstream response.
func validateProviderSourceConfiguration(source database.ProviderSource) (map[string]string, []string, error) {
	headers, sensitiveValues, err := source.RequestConfiguration()
	if err != nil {
		return nil, nil, ErrProviderSourceUnavailable
	}
	return headers, sensitiveValues, nil
}

// providerSourceHTTPClient retains the shared transport, timeout, and response
// limits used by node fetching. On a cross-host redirect it removes all
// ProviderSource credentials before the redirected request is sent.
func providerSourceHTTPClient(requestHeaders map[string]string, source database.ProviderSource) *http.Client {
	client := *fetchHTTPClient
	sourceURL, _ := url.Parse(source.SubscriptionURL)
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if sourceURL != nil && req.URL.Host == sourceURL.Host {
			return nil
		}

		for key := range requestHeaders {
			req.Header.Del(key)
		}
		req.Header.Del("X-HWID")
		req.Header.Del("User-Agent")

		return nil
	}

	return &client
}

func containsSensitiveProviderValue(value string, sensitiveValues []string) bool {
	for _, sensitive := range sensitiveValues {
		if sensitive != "" && strings.Contains(value, sensitive) {
			return true
		}
	}

	return false
}

// Format represents the detected encoding format of a subscription response body.
type Format int

const (
	// FormatUnknown means the body is empty or unparseable.
	FormatUnknown Format = iota
	// FormatJSON means the body is valid JSON (object or array).
	FormatJSON
	// FormatBase64 means the body is valid base64-encoded share links.
	FormatBase64
	// FormatPlain means the body contains plain-text share links.
	FormatPlain
	// FormatClash means the body is a Clash/Mihomo YAML config with a proxies section.
	FormatClash
)

// String returns the human-readable name of the format.
func (f Format) String() string {
	switch f {
	case FormatJSON:
		return "json"
	case FormatBase64:
		return "base64"
	case FormatPlain:
		return "plain"
	case FormatClash:
		return "clash"
	default:
		return "unknown"
	}
}

// DetectFormat examines body and returns its format: JSON, Clash, Base64, Plain, or Unknown.
func DetectFormat(body []byte) Format {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return FormatUnknown
	}

	if json.Valid([]byte(trimmed)) {
		return FormatJSON
	}

	if isClashYAML(body) {
		return FormatClash
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err == nil && len(decoded) > 0 && isValidSubscription(string(decoded)) {
		return FormatBase64
	}

	decoded, err = base64.RawStdEncoding.DecodeString(trimmed)
	if err == nil && len(decoded) > 0 && isValidSubscription(string(decoded)) {
		return FormatBase64
	}

	if isValidSubscription(trimmed) {
		return FormatPlain
	}

	return FormatUnknown
}

// isClashYAML checks whether body is a Clash/Mihomo YAML config by looking
// for a top-level "proxies" key. YAML is a superset of JSON, so this check
// must run after json.Valid.
func isClashYAML(body []byte) bool {
	var root map[string]yaml.Node

	err := yaml.Unmarshal(body, &root)
	if err != nil {
		return false
	}

	_, ok := root["proxies"]

	return ok
}

// isValidSubscription returns true if at least one line in data is a recognised share link.
func isValidSubscription(data string) bool {
	lines := strings.SplitSeq(data, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if isValidServer(line) {
			return true
		}
	}

	return false
}

// base64StdEncode is a short-hand for standard base64 encoding.
func base64StdEncode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
