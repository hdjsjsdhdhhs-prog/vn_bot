package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"

	"go.uber.org/zap"
)

type loadedSubscription struct {
	full           *database.SubscriptionFull
	providerSource *database.ProviderSource
	cacheKey       string
	cachedResult   *SubscriptionResult
}

// loadSubscription resolves the serving route under the per-subscription
// analytics lock. ProviderSource-backed subscriptions never load plan nodes;
// unlinked subscriptions continue through GetWithPlanAndNodes unchanged.
func loadSubscription(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID, clientIP string, requestHeaders map[string]string) (*loadedSubscription, error) {
	// The analytics update is a read-modify-write on the subscription row
	// (devices/IPs JSON). The freshest row must be loaded AFTER taking the lock
	// so concurrent cache misses for the SAME subID serialize the whole
	// read-modify-write and cannot overwrite each other's entries; requests for
	// different subIDs proceed in parallel. The lock is released via defer, so
	// it is guaranteed to be freed on error or panic.
	unlock := subSvc.analyticsLocks.Lock(subID)
	defer unlock()

	sub, err := db.GetSubscriptionWithProviderSource(ctx, subID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			subSvc.InvalidateCache(subID)
			metrics.SubserverCacheInvalidationsTotal.WithLabelValues("not_found").Inc()
			logger.Debug("Subscription not found in database",
				zap.String("sub_id", subID))

			return nil, ErrSubscriptionNotFound
		}

		logger.Error("Failed to resolve subscription serving route",
			zap.String("sub_id", subID),
			zap.Error(err))

		return nil, fmt.Errorf("database error: %w", err)
	}

	if sub.Status != string(database.SubscriptionStatusActive) ||
		(sub.ExpiresAt != nil && !sub.ExpiresAt.After(time.Now())) {
		subSvc.InvalidateCache(subID)

		invalidReason := string(database.SubscriptionStatusRevoked)
		if sub.Status == string(database.SubscriptionStatusActive) {
			invalidReason = "expired"
		}

		metrics.SubserverCacheInvalidationsTotal.WithLabelValues(invalidReason).Inc()
		logger.Warn("Cache invalidated: subscription no longer active",
			zap.String("sub_id", subID),
			zap.String("status", sub.Status),
			zap.Timep("expires_at", sub.ExpiresAt))

		return nil, ErrSubscriptionNotFound
	}

	loaded := &loadedSubscription{cacheKey: subID}
	if sub.ProviderSourceID != nil {
		if sub.ProviderSource == nil {
			subSvc.InvalidateCache(subID)
			logger.Warn("Linked provider source is missing",
				zap.String("sub_id", subID),
				zap.Uint("provider_source_id", *sub.ProviderSourceID))

			return nil, ErrProviderSourceUnavailable
		}

		if _, _, configErr := validateProviderSourceConfiguration(*sub.ProviderSource); configErr != nil {
			subSvc.InvalidateCache(subID)
			logger.Warn("Linked provider source is disabled or unusable",
				zap.String("sub_id", subID),
				zap.Uint("provider_source_id", *sub.ProviderSourceID))

			return nil, ErrProviderSourceUnavailable
		}

		loaded.providerSource = sub.ProviderSource
		loaded.cacheKey = providerSourceCacheKey(subID, *sub.ProviderSource)
	}

	if cachedBody, cachedHeaders, ok := subSvc.GetCache(loaded.cacheKey); ok {
		logger.Debug("Cache hit", zap.String("sub_id", subID))
		updateLastRequest(ctx, db, subID)
		loaded.cachedResult = &SubscriptionResult{Body: cachedBody, Headers: cachedHeaders}

		return loaded, nil
	}

	var subFull *database.SubscriptionFull
	if loaded.providerSource == nil {
		subFull, err = db.GetWithPlanAndNodes(ctx, subID)
		if err != nil {
			if errors.Is(err, database.ErrSubscriptionNotFound) {
				logger.Debug("Subscription not found in database",
					zap.String("sub_id", subID))

				return nil, ErrSubscriptionNotFound
			}

			logger.Error("Failed to get subscription with plan and sources",
				zap.String("sub_id", subID),
				zap.Error(err))

			return nil, fmt.Errorf("database error: %w", err)
		}
	} else {
		subFull = &database.SubscriptionFull{Subscription: *sub}
	}

	loaded.full = subFull

	logger.Debug("Subscription loaded from database",
		zap.Uint("sub_pk", subFull.Subscription.ID),
		zap.String("status", subFull.Subscription.Status),
		zap.Timep("expires_at", subFull.Subscription.ExpiresAt),
		zap.Bool("provider_source", loaded.providerSource != nil),
	)

	UpdateDevices(ctx, db, subFull, requestHeaders)
	UpdateIPs(ctx, db, subFull, clientIP)

	updateLastRequest(ctx, db, subID)

	return loaded, nil
}

func updateLastRequest(ctx context.Context, db interfaces.SubscriptionRepository, subID string) {
	err := db.UpdateLastRequest(ctx, subID)
	if err != nil {
		logger.Warn("Failed to update last_request",
			zap.String("sub_id", subID),
			zap.Error(err))
	}
}

func providerSourceCacheKey(subID string, source database.ProviderSource) string {
	return fmt.Sprintf("%s:provider:%d:%d", subID, source.ID, source.UpdatedAt.UnixNano())
}

// aggregatedSources holds the collected items and traffic data from all sources.
type aggregatedSources struct {
	items              []string
	jsonConfigs        []json.RawMessage
	firstExpire        string
	firstExpireVal     int64
	totalUpload        int64
	totalDownload      int64
	allJSON            bool
	firstSourceHeaders map[string]string
}

// maxSourceConcurrency bounds how many upstream sources are fetched in parallel
// so a single slow/down node cannot be amplified into unbounded connection usage.
const maxSourceConcurrency = 8

// sourceResult holds the outcome of fetching a single upstream node.
type sourceResult struct {
	source  database.Node
	body    []byte
	headers map[string]string
	format  Format
}

// fetchAndAggregateProviderSource fetches exactly one linked ProviderSource and
// feeds its payload through the same format detection and normalization used by
// legacy nodes. Provider fetch failures are terminal and never fall back to
// plan nodes.
func fetchAndAggregateProviderSource(ctx context.Context, subID string, source database.ProviderSource) (aggregatedSources, int, int, error) {
	fetchStart := time.Now()
	response, err := FetchFromProviderSource(ctx, source)
	fetchDuration := time.Since(fetchStart).Seconds()
	if err != nil {
		metrics.SubserverSourceFetchTotal.WithLabelValues("error", "unknown").Inc()
		metrics.SubserverSourceFetchDuration.WithLabelValues("error").Observe(fetchDuration)

		return aggregatedSources{}, 0, 1, ErrProviderSourceUnavailable
	}

	format := DetectFormat(response.Body)
	metrics.SubserverSourceFetchTotal.WithLabelValues("success", format.String()).Inc()
	metrics.SubserverSourceFetchDuration.WithLabelValues("success").Observe(fetchDuration)
	logger.Debug("Provider source response received",
		zap.String("sub_id", subID),
		zap.Uint("provider_source_id", source.ID),
		zap.String("format", format.String()),
		zap.Int("body_size", len(response.Body)),
		zap.Int("headers_count", len(response.Headers)))

	agg := aggregatedSources{
		allJSON:            true,
		firstSourceHeaders: response.Headers,
	}
	updateMinExpire(&agg, response.Headers)
	agg.totalUpload = ParseUserInfoValue(response.Headers, "upload")
	agg.totalDownload = ParseUserInfoValue(response.Headers, "download")

	aggregateFormat(&agg, format, response.Body, database.Node{
		ID:   source.ID,
		Name: source.Name,
	}, subID)

	return agg, 1, 1, nil
}

// fetchAndAggregateSources fetches all active source nodes concurrently (bounded
// by maxSourceConcurrency) and aggregates their items and traffic counters. The
// per-source fetch is isolated: a failure logs and is skipped, never aborting the
// others. Results are merged in source order to keep header/expire selection
// deterministic.
func fetchAndAggregateSources(ctx context.Context, subID string, nodes []database.Node) (aggregatedSources, int, int) {
	agg := aggregatedSources{
		allJSON: true,
	}

	results := make([]sourceResult, len(nodes))
	sem := make(chan struct{}, maxSourceConcurrency)

	var wg sync.WaitGroup

	for i := range nodes {
		src := nodes[i]
		if src.SubscriptionURL == "" {
			logger.Warn("Skipping node without subscription_url",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
			)

			continue
		}

		if ctx.Err() != nil {
			break
		}

		wg.Add(1)

		sem <- struct{}{}

		go func(idx int, src database.Node) {
			defer wg.Done()
			defer func() { <-sem }()

			results[idx] = fetchSource(ctx, subID, src)
		}(i, src)
	}

	wg.Wait()

	successCount := 0
	totalCount := 0

	for i := range nodes {
		if nodes[i].SubscriptionURL == "" {
			continue
		}

		totalCount++

		if results[i].body != nil {
			successCount++
		}
	}

	for i := range nodes {
		res := results[i]
		if res.body == nil {
			continue
		}
		// Capture headers from the first successful source for client replay.
		if agg.firstSourceHeaders == nil {
			agg.firstSourceHeaders = res.headers
		}

		updateMinExpire(&agg, res.headers)

		// Aggregate usage counters across all sources.
		agg.totalUpload += ParseUserInfoValue(res.headers, "upload")
		agg.totalDownload += ParseUserInfoValue(res.headers, "download")

		aggregateFormat(&agg, res.format, res.body, res.source, subID)
	}

	return agg, successCount, totalCount
}

// fetchSource performs a single upstream fetch, records metrics, and returns the
// parsed result. On error it returns a zero sourceResult (nil body), which the
// caller treats as "skip this source".
func fetchSource(ctx context.Context, subID string, src database.Node) sourceResult {
	sourceURL := buildSourceURL(src, subID)

	fetchStart := time.Now()
	srcResp, err := FetchFromNode(ctx, sourceURL)
	fetchDuration := time.Since(fetchStart).Seconds()

	if err != nil {
		metrics.SubserverSourceFetchTotal.WithLabelValues("error", "unknown").Inc()
		metrics.SubserverSourceFetchDuration.WithLabelValues("error").Observe(fetchDuration)
		logger.Error("Failed to fetch from node",
			zap.String("sub_id", subID),
			zap.String("source", src.Name),
			zap.String("node_url", sourceURL),
			zap.Error(err))

		return sourceResult{}
	}

	body := srcResp.Body

	srcHeaders := srcResp.Headers
	if srcHeaders == nil {
		srcHeaders = make(map[string]string)
	}

	format := DetectFormat(body)
	metrics.SubserverSourceFetchTotal.WithLabelValues("success", format.String()).Inc()
	metrics.SubserverSourceFetchDuration.WithLabelValues("success").Observe(fetchDuration)
	logger.Debug("Node response received",
		zap.String("sub_id", subID),
		zap.String("source", src.Name),
		zap.String("format", format.String()),
		zap.Int("body_size", len(body)),
		zap.Int("headers_count", len(srcHeaders)),
	)

	return sourceResult{
		source:  src,
		body:    body,
		headers: srcHeaders,
		format:  format,
	}
}

// buildSourceURL constructs the upstream fetch URL for a node, handling
// fetch-type nodes (use URL as-is) versus regular nodes (append subID).
func buildSourceURL(src database.Node, subID string) string {
	if src.Type == database.NodeTypeFetch {
		return src.SubscriptionURL
	}

	srcSubURL := src.SubscriptionURL
	if !strings.HasSuffix(srcSubURL, "/") {
		srcSubURL += "/"
	}

	return srcSubURL + subID
}

// aggregateFormat parses the upstream body according to its detected format
// and appends items or JSON configs to the aggregator.
func aggregateFormat(agg *aggregatedSources, format Format, body []byte, src database.Node, subID string) {
	switch format {
	case FormatJSON:
		configs, parseErr := ExtractJSONConfigs(body)
		if parseErr != nil {
			logger.Error("Failed to parse JSON configs from node",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
				zap.Error(parseErr))

			agg.allJSON = false

			return
		}

		agg.jsonConfigs = append(agg.jsonConfigs, configs...)
	case FormatClash:
		// Clash is not a pure-JSON source, so force the base64/link output path.
		agg.allJSON = false

		configs, parseErr := ExtractClashConfigs(body)
		if parseErr != nil {
			logger.Error("Failed to parse Clash configs from node",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
				zap.Error(parseErr))

			return
		}

		agg.jsonConfigs = append(agg.jsonConfigs, configs...)
	case FormatBase64:
		agg.allJSON = false

		encoded := strings.TrimSpace(string(body))
		decoded, decErr := base64.StdEncoding.DecodeString(encoded)
		if decErr != nil {
			decoded, decErr = base64.RawStdEncoding.DecodeString(encoded)
		}
		if decErr != nil {
			agg.items = append(agg.items, encoded)
		} else {
			for line := range strings.SplitSeq(string(decoded), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					agg.items = append(agg.items, line)
				}
			}
		}
	case FormatPlain:
		agg.allJSON = false

		for line := range strings.SplitSeq(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				agg.items = append(agg.items, line)
			}
		}
	}
}

// updateMinExpire records the earliest expiry seen across sources. Expiry
// values are normalized to seconds before comparison so that seconds and
// milliseconds widths are ordered correctly.
func updateMinExpire(agg *aggregatedSources, srcHeaders map[string]string) {
	userInfo, ok := srcHeaders["subscription-userinfo"]
	if !ok {
		return
	}

	exp, ok := parseExpireToInt(userInfo)
	if !ok {
		return
	}

	if agg.firstExpireVal == 0 || exp < agg.firstExpireVal {
		agg.firstExpireVal = exp
		agg.firstExpire = strconv.FormatInt(exp, 10)
	}
}

// parseExpireToInt extracts the "expire=" value from a subscription-userinfo
// header and parses it as an int64 (unix seconds). Values in milliseconds are
// normalized to seconds so sources using different widths compare correctly.
// Non-numeric expires are ignored because they cannot be compared reliably.
func parseExpireToInt(userInfo string) (int64, bool) {
	raw := ParseExpireFromUserInfo(userInfo)
	if raw == "" {
		return 0, false
	}

	exp, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}

	if exp > 1e11 {
		exp /= 1000
	}

	return exp, true
}

// buildResponse takes the aggregated source data and constructs the final
// subscription response body with headers, writing it to cache. When
// profileTitleSuffix is non-empty it is appended (base64-aware) to the upstream
// profile-title header before the response is cached.
func buildResponse(subSvc *Service, cacheKey string, agg aggregatedSources, trafficLimit int64, profileTitleSuffix string) (*SubscriptionResult, error) {
	userInfo := BuildUserInfoHeader(agg.totalUpload, agg.totalDownload, trafficLimit, agg.firstExpire)

	// If we are in mixed mode (some sources returned non-JSON),
	// convert any collected JSON configs to share links and merge into items.
	if !agg.allJSON && len(agg.jsonConfigs) > 0 {
		for _, rawConfig := range agg.jsonConfigs {
			link, convErr := ConvertSingleJSONToLink(rawConfig)
			if convErr != nil {
				logger.Error("Failed to convert JSON config to share link",
					zap.Error(convErr))

				continue
			}

			agg.items = append(agg.items, link)
		}
	}

	// Pure-JSON output: marshal all raw serverConfig objects into a JSON
	// array response.
	if agg.allJSON && len(agg.jsonConfigs) > 0 {
		responseBody, marshalErr := json.Marshal(agg.jsonConfigs)
		if marshalErr != nil {
			logger.Error("Failed to marshal JSON response",
				zap.Error(marshalErr))

			return nil, fmt.Errorf("failed to marshal response: %w", marshalErr)
		}

		cacheHeaders := ResponseHeaders(agg.firstSourceHeaders, "application/json; charset=utf-8", userInfo)
		ApplyProfileTitleSuffix(cacheHeaders, profileTitleSuffix)
		subSvc.SetCache(cacheKey, responseBody, cacheHeaders)

		return &SubscriptionResult{
			Body:    responseBody,
			Headers: cacheHeaders,
		}, nil
	}

	// No servers collected from any source.
	if len(agg.items) == 0 {
		metrics.SubserverNoItemsTotal.Inc()
		return nil, ErrNoSubscriptionItems
	}

	// Mixed or plain-text output: join all share links and encode to base64.
	combined := strings.Join(agg.items, "\n")
	responseBody := []byte(base64.StdEncoding.EncodeToString([]byte(combined)))
	ct := "text/plain; charset=utf-8; profile=base64"
	cacheHeaders := ResponseHeaders(agg.firstSourceHeaders, ct, userInfo)
	ApplyProfileTitleSuffix(cacheHeaders, profileTitleSuffix)
	subSvc.SetCache(cacheKey, responseBody, cacheHeaders)

	return &SubscriptionResult{
		Body:    responseBody,
		Headers: cacheHeaders,
	}, nil
}
