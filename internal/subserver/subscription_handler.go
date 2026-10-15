// Package subserver implements the subscription delivery endpoint (/sub/:id).
// A subscription is assembled by its SubscriptionBuilder from the catalogues of
// the builder's ProviderSources (ready-made upstream subscription URLs); a
// subscription linked directly to one ProviderSource and without a builder is
// served from that source. Responses are cached and requesting devices/IPs are
// tracked for analytics.
package subserver

import (
	"context"
	"maps"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// HandleSubscription processes a subscription request from cache or upstream sources.
// It returns the aggregated response body with headers, or an error if the subscription
// cannot be served.
//
// The flow is:
//  1. Resolve the serving route: the enabled builder (subscription override >
//     plan default), otherwise a directly linked ProviderSource, otherwise
//     ErrBuilderUnavailable (503, configuration error — no other fallback).
//  2. Check the route-specific response cache.
//  3. On a cache miss, track device/IP analytics.
//  4. Fetch and assemble through the builder or the single ProviderSource.
//  5. Normalize through the shared aggregation pipeline and cache the result.
func HandleSubscription(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID, clientIP string, requestHeaders map[string]string) (*SubscriptionResult, int, int, error) {
	loaded, err := loadSubscription(ctx, db, subSvc, subID, clientIP, requestHeaders)
	if err != nil {
		return nil, 0, 0, err
	}
	if loaded.cachedResult != nil {
		return loaded.cachedResult, 0, 0, nil
	}

	// Paid (premium) subscriptions get
	// a " Premium" suffix appended to the upstream profile-title header.
	profileTitleSuffix := ""
	if loaded.sub.IsPaid() {
		profileTitleSuffix = " Premium"
	}

	if loaded.builder != nil {
		built, success, total, buildErr := fetchAndAggregateBuilder(ctx, db, subID, loaded.builder)
		if buildErr != nil {
			return nil, success, total, buildErr
		}

		res, respErr := buildResponse(subSvc, loaded.cacheKey, built.agg, built.trafficLimit, profileTitleSuffix)

		return res, success, total, respErr
	}

	// loadSubscription guarantees a provider source when there is no builder.
	agg, success, total, fetchErr := fetchAndAggregateProviderSource(ctx, subID, *loaded.providerSource)
	if fetchErr != nil {
		return nil, success, total, fetchErr
	}

	upstreamTrafficLimit := ParseUserInfoValue(agg.firstSourceHeaders, "total")
	res, buildErr := buildResponse(subSvc, loaded.cacheKey, agg, upstreamTrafficLimit, profileTitleSuffix)

	return res, success, total, buildErr
}

// UpdateDevices records the current request headers as a device entry in the
// subscription's Devices JSON field. Each entry includes a "timestamp" key
// (UTC RFC3339) marking when the device was last seen. If an existing entry
// has the same x-hwid value it is replaced (rotated to the end). The updated
// list is persisted to DB. Devices are analytics only: no device limit is
// enforced.
func UpdateDevices(ctx context.Context, db interfaces.SubscriptionRepository, sub *database.Subscription, headers map[string]string) {
	devices, err := sub.ParseDevices()
	if err != nil {
		logger.Error("Failed to parse devices JSON",
			zap.Uint("sub_pk", sub.ID),
			zap.String("sub_id", sub.SubscriptionID),
			zap.Error(err))

		devices = []map[string]string{}
	}

	if headers != nil {
		currentHWID := headers["x-hwid"]
		nowStr := time.Now().UTC().Format(time.RFC3339)

		for i, dev := range devices {
			if dev["x-hwid"] == currentHWID {
				devices = append(devices[:i], devices[i+1:]...)
				break
			}
		}

		entry := make(map[string]string, len(headers)+1)
		maps.Copy(entry, headers)

		entry["timestamp"] = nowStr
		devices = append(devices, entry)

		if len(devices) > MaxDeviceEntries {
			devices = devices[len(devices)-MaxDeviceEntries:]
		}
	}

	err = sub.SetDevices(devices)
	if err != nil {
		logger.Error("Failed to set devices JSON",
			zap.Uint("sub_pk", sub.ID),
			zap.String("sub_id", sub.SubscriptionID),
			zap.Error(err))

		return
	}

	err = db.UpdateDevices(ctx, sub.ID, sub.Devices)
	if err != nil {
		logger.Error("Failed to save devices to database",
			zap.Uint("sub_pk", sub.ID),
			zap.String("sub_id", sub.SubscriptionID),
			zap.Error(err))
	}
}

// UpdateIPs records the current client IP with a UTC timestamp in the
// subscription's Ips JSON field. Duplicate IPs are rotated to the end.
// The list is capped at maxIPEntries (oldest entries are dropped).
func UpdateIPs(ctx context.Context, db interfaces.SubscriptionRepository, sub *database.Subscription, ip string) {
	ips, err := sub.ParseIPs()
	if err != nil {
		logger.Error("Failed to parse ips JSON",
			zap.Uint("sub_pk", sub.ID),
			zap.String("sub_id", sub.SubscriptionID),
			zap.Error(err))

		ips = []map[string]string{}
	}

	if ip != "" {
		nowStr := time.Now().UTC().Format(time.RFC3339)

		for i, entry := range ips {
			if _, exists := entry[ip]; exists {
				ips = append(ips[:i], ips[i+1:]...)
				break
			}
		}

		newEntry := map[string]string{ip: nowStr}
		ips = append(ips, newEntry)

		if len(ips) > MaxIPEntries {
			ips = ips[len(ips)-MaxIPEntries:]
		}
	}

	err = sub.SetIPs(ips)
	if err != nil {
		logger.Error("Failed to set ips JSON",
			zap.Uint("sub_pk", sub.ID),
			zap.String("sub_id", sub.SubscriptionID),
			zap.Error(err))

		return
	}

	err = db.UpdateIPs(ctx, sub.ID, sub.Ips)
	if err != nil {
		logger.Error("Failed to save ips to database",
			zap.Uint("sub_pk", sub.ID),
			zap.String("sub_id", sub.SubscriptionID),
			zap.Error(err))
	}
}
