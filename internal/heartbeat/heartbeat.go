package heartbeat

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

var (
	httpClientMu sync.Mutex
	httpClient   *http.Client
)

// getHTTPClient returns a shared HTTP client with optimized transport for minimal memory.
// The client is created once and reused for all heartbeat requests.
func getHTTPClient() *http.Client {
	httpClientMu.Lock()
	defer httpClientMu.Unlock()

	if httpClient == nil {
		transport := &http.Transport{
			MaxIdleConns:        config.MaxIdleConns,
			MaxIdleConnsPerHost: config.MaxIdleConns,
			IdleConnTimeout:     config.DefaultIdleConnTimeout,
			DisableCompression:  false,
			ForceAttemptHTTP2:   false,
		}
		httpClient = &http.Client{
			Timeout:   config.DefaultHTTPTimeout,
			Transport: transport,
		}
	}

	return httpClient
}

// resetHTTPClient resets the shared HTTP client.
// This function is intended for testing purposes only.
func resetHTTPClient() {
	httpClientMu.Lock()
	defer httpClientMu.Unlock()

	httpClient = nil
}

// Start begins sending periodic heartbeat POST requests to the specified URL.
// If url is empty, no heartbeat signals are sent.
// The function runs until the context is cancelled.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: The heartbeat endpoint URL (optional)
//   - intervalSeconds: Interval between heartbeats in seconds (minimum: config.MinHeartbeatInterval)
func Start(ctx context.Context, url string, intervalSeconds int) {
	if url == "" {
		logger.Info("Heartbeat URL not configured, skipping heartbeat scheduler")
		return
	}

	// Validate and normalize interval
	if intervalSeconds < config.MinHeartbeatInterval {
		logger.Warn("Heartbeat interval is too low, using minimum",
			zap.Int("requested", intervalSeconds),
			zap.Int("minimum", config.MinHeartbeatInterval))
		intervalSeconds = config.MinHeartbeatInterval
	}

	interval := time.Duration(intervalSeconds) * time.Second
	logger.Info("Heartbeat scheduler started",
		zap.String("url", maskURL(url)),
		zap.Duration("interval", interval))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Send initial heartbeat
	sendHeartbeat(url)

	for {
		select {
		case <-ticker.C:
			sendHeartbeat(url)
		case <-ctx.Done():
			logger.Info("Heartbeat scheduler stopped")
			return
		}
	}
}

// sendHeartbeat sends a POST request to the heartbeat URL.
// Errors are logged but do not cause the scheduler to stop.
func sendHeartbeat(url string) {
	client := getHTTPClient()

	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		logger.Error("Heartbeat failed", zap.Error(err))
		return
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logger.Debug("Failed to close heartbeat response body", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Debug("Heartbeat sent successfully")
	} else {
		logger.Warn("Heartbeat returned non-success status", zap.Int("status_code", resp.StatusCode))
	}
}

// maskURL hides the complete heartbeat URL, including userinfo and query.
func maskURL(urlStr string) string {
	return logger.SafeURL(urlStr)
}
