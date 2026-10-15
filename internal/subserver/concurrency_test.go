package subserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func TestFetchAndAggregateBuilder_Parallel(t *testing.T) {
	// 10 builder sources, each sleeping for 100ms.
	// Sequential: 1000ms.
	// Parallel (concurrency 8): ~200ms.
	numSources := 10
	sleepTime := 100 * time.Millisecond

	sources := make([]*database.ProviderSource, numSources)
	for i := range numSources {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Intentional delay: simulates upstream work so the test can verify
			// that sources are fetched in parallel (not sequentially).
			time.Sleep(sleepTime)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}))
		defer ts.Close()

		sources[i] = testSource(uint(i+1), ts.URL)
	}

	start := time.Now()
	ctx := context.Background()
	agg, _, _, err := fetchAndAggregateBuilder(ctx, testutil.NewDatabaseService(), "test", testBuilder(1, sources))
	duration := time.Since(start)
	// Only the wall-clock time is under test here.
	_, _ = agg, err

	// Verify parallel execution (significantly less than sequential)
	assert.Less(t, duration, 800*time.Millisecond, "Should be faster than sequential execution")
	// Verify it's not too fast (respects concurrency constraints)
	assert.GreaterOrEqual(t, duration, 150*time.Millisecond, "Should respect concurrency limits")
}
