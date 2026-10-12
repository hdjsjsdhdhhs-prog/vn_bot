package web

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Builder list/read contract (/admin/api/builders*) against the real SQLite
// repository and the real catalogue sync. Regression: the list used to return
// builders without their sources and rules, so the admin panel showed
// "0 источника · 0 правила" for configured builders and the editor, opened
// from that list copy, saved the builder back with an empty source list.

func builderPath(id uint, suffix string) string {
	return "/admin/api/builders/" + strconv.FormatUint(uint64(id), 10) + suffix
}

func TestAdminBuilderAPI_ListAndGetCarryStoredSourcesAndRules(t *testing.T) {
	f := newSourceAPIFixture(t)
	f.login()

	createSource := func(name string) service.SourceView {
		resp := f.do(http.MethodPost, "/admin/api/sources", f.withCSRF(), withJSON(f.createBody(name, "")))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created struct {
			Source service.SourceView `json:"source"`
		}
		require.NoError(t, jsonUnmarshal(f.text(resp), &created))
		require.GreaterOrEqual(t, len(created.Source.Catalogue.ByCountry), 2)
		return created.Source
	}
	srcA := createSource("Provider A")
	srcB := createSource("Provider B")

	// A new builder: sources and items are present as [] (not omitted).
	resp := f.do(http.MethodPost, "/admin/api/builders", f.withCSRF(), withJSON(
		`{"request_key":"req-create","name":"FINLAN","description":"Финляндия","enabled":true,"profile_title":"","support_url":"","announce":""}`))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		Builder service.BuilderView `json:"builder"`
	}
	raw := f.text(resp)
	require.NoError(t, jsonUnmarshal(raw, &created))
	assert.Contains(t, raw, `"sources":[]`)
	assert.Contains(t, raw, `"items":[]`)
	id := created.Builder.ID

	list := func() service.BuilderView {
		t.Helper()
		raw := f.text(f.do(http.MethodGet, "/admin/api/builders"))
		var out struct {
			Builders []service.BuilderView `json:"builders"`
		}
		require.NoError(t, jsonUnmarshal(raw, &out))
		require.Len(t, out.Builders, 1)
		return out.Builders[0]
	}
	get := func() service.BuilderView {
		t.Helper()
		var out service.BuilderView
		require.NoError(t, jsonUnmarshal(f.text(f.do(http.MethodGet, builderPath(id, ""))), &out))
		return out
	}
	assert.Empty(t, list().Sources)

	// Two sources, two country rules on A, one node rule on B.
	resp = f.do(http.MethodPut, builderPath(id, "/sources"), f.withCSRF(), withJSON(fmt.Sprintf(`{"source_ids":[%d,%d]}`, srcA.ID, srcB.ID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.text(resp)

	c1, c2 := srcA.Catalogue.ByCountry[0], srcA.Catalogue.ByCountry[1]
	var entries struct {
		Entries []database.ProviderSourceEntry `json:"entries"`
	}
	require.NoError(t, jsonUnmarshal(f.text(f.do(http.MethodGet, sourcePath(srcB.ID, "/entries"))), &entries))
	require.NotEmpty(t, entries.Entries)
	node := entries.Entries[0]

	for i, body := range []string{
		fmt.Sprintf(`{"kind":"country","source_id":%d,"country_code":%q,"position":0,"enabled":true}`, srcA.ID, c1.Code),
		fmt.Sprintf(`{"kind":"country","source_id":%d,"country_code":%q,"position":1,"enabled":true}`, srcA.ID, c2.Code),
		fmt.Sprintf(`{"kind":"node","source_id":%d,"fingerprint":%q,"original_name":%q,"position":2,"enabled":true}`, srcB.ID, node.Fingerprint, node.OriginalName),
	} {
		resp := f.do(http.MethodPost, builderPath(id, "/items"), f.withCSRF(), withJSON(body))
		require.Equal(t, http.StatusOK, resp.StatusCode, "item %d", i)
		f.text(resp)
	}

	assertConfigured := func(v service.BuilderView, desc, label string) {
		t.Helper()
		assert.Equal(t, desc, v.Description, label)
		require.Len(t, v.Sources, 2, label)
		assert.Equal(t, []service.BuilderSourceView{{SourceID: srcA.ID, Position: 0}, {SourceID: srcB.ID, Position: 1}}, v.Sources, label)
		require.Len(t, v.Items, 3, label)
		assert.Equal(t, c1.Code, v.Items[0].CountryCode, label)
		assert.Equal(t, c2.Code, v.Items[1].CountryCode, label)
		assert.Equal(t, database.BuilderItemKindNode, v.Items[2].Kind, label)
		assert.Equal(t, srcB.ID, v.Items[2].SourceID, label)
		assert.Equal(t, node.Fingerprint, v.Items[2].Fingerprint, label)
	}
	assertConfigured(list(), "Финляндия", "list")
	assertConfigured(get(), "Финляндия", "get")

	// A metadata save (the editor's PATCH) keeps sources and rules.
	current := get()
	resp = f.do(http.MethodPatch, builderPath(id, ""), f.withCSRF(), withJSON(fmt.Sprintf(
		`{"request_key":"req-update","version":%d,"name":"FINLAN","description":"Финляндия и соседи","enabled":true,"profile_title":"","support_url":"","announce":""}`,
		current.Version)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.text(resp)
	updated := list()
	assertConfigured(updated, "Финляндия и соседи", "list after update")
	assert.Equal(t, current.Version+1, updated.Version)

	// Disable / enable answer with the full record too.
	for _, action := range []string{"/disable", "/enable"} {
		resp := f.do(http.MethodPost, builderPath(id, action), f.withCSRF())
		require.Equal(t, http.StatusOK, resp.StatusCode, action)
		var toggled service.BuilderView
		require.NoError(t, jsonUnmarshal(f.text(resp), &toggled))
		assert.Len(t, toggled.Sources, 2, action)
		assert.Len(t, toggled.Items, 3, action)
		assert.Equal(t, action == "/enable", toggled.Enabled, action)
	}

	// The stored rows match what the API reports.
	var stored []database.SubscriptionBuilderItem
	require.NoError(t, f.db.GetDB().Where("builder_id = ?", id).Order("position ASC").Find(&stored).Error)
	require.Len(t, stored, 3)
	assert.Equal(t, []string{c1.Code, c2.Code, ""}, []string{stored[0].CountryCode, stored[1].CountryCode, stored[2].CountryCode})

	// The preview (same resolver family as /sub) serves exactly those countries
	// from A plus the one node of B.
	resp = f.do(http.MethodPost, builderPath(id, "/preview"), f.withCSRF())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var preview database.PreviewBuilderResult
	require.NoError(t, jsonUnmarshal(f.text(resp), &preview))
	assert.Equal(t, c1.Count+c2.Count+1, preview.Total)
	assert.Zero(t, preview.Missing)
	for _, it := range preview.Items {
		require.NotNil(t, it.Entry)
		switch it.SourceID {
		case srcA.ID:
			assert.Contains(t, []string{c1.Code, c2.Code}, it.Entry.CountryCode)
		case srcB.ID:
			assert.Equal(t, node.Fingerprint, it.Entry.Fingerprint)
		default:
			t.Fatalf("unexpected source %d in preview", it.SourceID)
		}
	}
}
