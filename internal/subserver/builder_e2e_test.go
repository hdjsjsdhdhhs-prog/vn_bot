package subserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuilder_E2E_SelectionPriorityAndInvalidation drives /sub through the
// real SQLite repository: existing pipeline → plan default → subscription
// override → builder update → disabled override → cleared plan default.
func TestBuilder_E2E_SelectionPriorityAndInvalidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, err := database.NewService(filepath.Join(t.TempDir(), "builder-e2e.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	keySeq := 0
	meta := func() database.AdminConfigMeta {
		keySeq++
		return database.AdminConfigMeta{Actor: "test-admin", RequestKey: fmt.Sprintf("req-%d", keySeq)}
	}

	newSource := func(name, link string) (*database.ProviderSource, *builderTestUpstream) {
		up := newBuilderTestUpstream(t, http.StatusOK, nil, link)
		src := &database.ProviderSource{Name: name, Type: "external", SubscriptionURL: up.srv.URL, Headers: `{}`, Enabled: true}
		require.NoError(t, svc.CreateProviderSource(ctx, src))
		return src, up
	}

	newBuilder := func(name string, sources ...uint) uint {
		res, err := svc.CreateBuilder(ctx, meta(), database.BuilderCreateInput{Name: name, Enabled: true})
		require.NoError(t, err)
		require.NoError(t, svc.SetBuilderSources(ctx, res.TargetID, sources))
		return res.TargetID
	}

	legacySrc, _ := newSource("legacy-provider", testLink("u", "provider.example", "Provider"))
	planSrc, _ := newSource("plan-source", testLink("u", "plan.example", "PlanNode"))
	subSrc, subUp := newSource("sub-source", testLink("u", "sub.example", "SubNode"))

	plan, err := svc.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)

	sub := &database.Subscription{
		TelegramID:       424242,
		ClientID:         "client-builder-e2e",
		SubscriptionID:   "builder-e2e-sub",
		ExpiresAt:        timePointer(time.Now().Add(time.Hour)),
		Status:           string(database.SubscriptionStatusActive),
		PlanID:           plan.ID,
		ProviderSourceID: &legacySrc.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	subSvc := newTestSubSvc(t)
	serve := func() *SubscriptionResult {
		t.Helper()
		result, _, _, err := HandleSubscription(ctx, svc, subSvc, sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		return result
	}
	names := func() []string { return linkNames(decodeBuilderLinks(t, serve())) }

	// 1. No builder anywhere → existing ProviderSource pipeline.
	assert.Equal(t, []string{"Provider"}, names())

	// 2. Plan default builder.
	planBuilder := newBuilder("plan-builder", planSrc.ID)
	_, err = svc.SetPlanBuilder(ctx, meta(), plan.ID, &planBuilder)
	require.NoError(t, err)
	assert.Equal(t, []string{"PlanNode"}, names())

	// 3. Subscription override wins over the plan default.
	subBuilder := newBuilder("sub-builder", subSrc.ID)
	_, err = svc.SetSubscriptionBuilder(ctx, meta(), sub.ID, &subBuilder)
	require.NoError(t, err)
	assert.Equal(t, []string{"SubNode"}, names())
	hitsBefore := subUp.hits.Load()
	assert.Equal(t, []string{"SubNode"}, names())
	assert.Equal(t, hitsBefore, subUp.hits.Load(), "unchanged builder must be served from cache")

	// 4. Builder update bumps the version → cache miss, new metadata served.
	current, err := svc.GetBuilder(ctx, subBuilder)
	require.NoError(t, err)
	_, err = svc.UpdateBuilder(ctx, meta(), database.BuilderUpdateInput{
		ID: subBuilder, Version: current.Version, Name: current.Name, Enabled: true, ProfileTitle: "RS8",
	})
	require.NoError(t, err)
	result := serve()
	assert.Equal(t, hitsBefore+1, subUp.hits.Load())
	assert.Equal(t, "base64:"+base64.StdEncoding.EncodeToString([]byte("RS8")), headerValue(result.Headers, "profile-title"))

	// 4b. Item rule change (not versioned) also invalidates the cache.
	_, err = svc.UpsertBuilderItem(ctx, database.SubscriptionBuilderItem{
		BuilderID: subBuilder, Kind: database.BuilderItemKindNode, SourceID: subSrc.ID,
		OriginalName: "SubNode", CustomName: strPtr("Renamed"), Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Renamed"}, names())

	// 5. Disabled override → plan default.
	_, err = svc.SetBuilderEnabled(ctx, subBuilder, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"PlanNode"}, names())

	// 6. Cleared plan default → existing pipeline again.
	_, err = svc.SetPlanBuilder(ctx, meta(), plan.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"Provider"}, names())
}
