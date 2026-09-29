package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BuilderService catalogue sync against the real SQLite repository with a
// scripted fetcher (the production fetcher is subserver.FetchSourceCatalogue,
// covered with a real provider fixture in internal/subserver).

type codedErr string

func (e codedErr) Error() string    { return "sync failed: " + string(e) }
func (e codedErr) SyncCode() string { return string(e) }

type scriptedFetcher struct {
	mu     sync.Mutex
	next   func() (*FetchedCatalogue, error)
	calls  int
	gate   chan struct{}
	gotURL string
}

func (f *scriptedFetcher) fetch(ctx context.Context, src database.ProviderSource) (*FetchedCatalogue, error) {
	f.mu.Lock()
	f.calls++
	f.gotURL = src.SubscriptionURL
	next, gate := f.next, f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return next()
}

func entriesOf(names ...[3]string) []database.ProviderSourceEntry {
	out := make([]database.ProviderSourceEntry, 0, len(names))
	for _, n := range names {
		out = append(out, database.ProviderSourceEntry{Fingerprint: n[0], OriginalName: n[1], CountryCode: n[2], Protocol: "vless"})
	}
	return out
}

func newCatalogueService(t *testing.T) (*BuilderService, *scriptedFetcher, *database.Service) {
	t.Helper()
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	svc := NewBuilderService(db)
	f := &scriptedFetcher{}
	svc.SetCatalogueFetcher(f.fetch)
	return svc, f, db
}

func validSourceInput(name string) CreateSourceInput {
	return CreateSourceInput{Name: name, Type: "", SubscriptionURL: "https://provider.example/sub/" + name, Headers: "", Enabled: true}
}

func TestBuilderService_CreateSourceRunsInitialSync(t *testing.T) {
	t.Parallel()
	svc, f, _ := newCatalogueService(t)
	f.next = func() (*FetchedCatalogue, error) {
		return &FetchedCatalogue{Format: "json", Entries: entriesOf(
			[3]string{"fp-ch1", "🇨🇭 1", "CH"}, [3]string{"fp-ch2", "🇨🇭 2", "CH"}, [3]string{"fp-de", "🇩🇪", "DE"},
		)}, nil
	}

	view, sync, err := svc.CreateSource(context.Background(), validSourceInput("initial"))
	require.NoError(t, err)
	require.NotNil(t, sync, "the initial sync runs on create")
	assert.Equal(t, 1, f.calls)
	assert.Equal(t, "https://provider.example/sub/initial", f.gotURL, "the fetcher gets the stored URL; the view never does")
	assert.Equal(t, database.SourceSyncOK, sync.Status)
	assert.Equal(t, "json", sync.Format)
	assert.Equal(t, database.SourceSyncCounts{Added: 3, Total: 3}, sync.SourceSyncCounts)
	assert.Equal(t, database.SourceFormatAuto, view.Type, "empty type is auto")
	assert.Equal(t, 3, view.Catalogue.Entries)
	assert.Equal(t, 2, view.Catalogue.Countries)
	assert.Equal(t, []database.SourceCountryCount{{Code: "CH", Count: 2}, {Code: "DE", Count: 1}}, view.Catalogue.ByCountry)
	require.NotNil(t, view.LastSyncAt, "an initially synced source is not 'never synced'")
}

func TestBuilderService_SyncRefreshFailureAndPartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, f, _ := newCatalogueService(t)
	f.next = func() (*FetchedCatalogue, error) {
		return &FetchedCatalogue{Format: "base64", Entries: entriesOf([3]string{"a", "🇩🇪 A", "DE"}, [3]string{"b", "🇳🇱 B", "NL"})}, nil
	}
	view, _, err := svc.CreateSource(ctx, validSourceInput("refresh"))
	require.NoError(t, err)

	// Refresh: one node added, one gone.
	f.next = func() (*FetchedCatalogue, error) {
		return &FetchedCatalogue{Format: "base64", Entries: entriesOf([3]string{"a", "🇩🇪 A", "DE"}, [3]string{"c", "🇯🇵 C", "JP"})}, nil
	}
	res, err := svc.SyncSource(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, database.SourceSyncCounts{Added: 1, Updated: 1, Removed: 1, Total: 2}, res.SourceSyncCounts)

	// Upstream down: status error with a stable code, catalogue kept.
	f.next = func() (*FetchedCatalogue, error) { return nil, codedErr("http_503") }
	res, err = svc.SyncSource(ctx, view.ID)
	require.NoError(t, err, "a failed fetch is a recorded outcome, not a request error")
	assert.Equal(t, database.SourceSyncError, res.Status)
	assert.Equal(t, "http_503", res.Error)
	assert.Equal(t, 2, res.Total, "the previous catalogue is kept")
	assert.Equal(t, 2, res.Source.Catalogue.Entries)
	assert.Equal(t, database.SourceSyncError, res.Source.LastSyncStatus)
	assert.Equal(t, "http_503", res.Source.LastSyncError)

	// An uncoded fetcher error is reported as "internal" (never its text).
	f.next = func() (*FetchedCatalogue, error) {
		return nil, errors.New("dial tcp https://secret.example/x: refused")
	}
	res, err = svc.SyncSource(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, "internal", res.Error)

	// Partial parse: the parsed entries are stored, the rest is reported.
	f.next = func() (*FetchedCatalogue, error) {
		return &FetchedCatalogue{Format: "json", Entries: entriesOf([3]string{"a", "🇩🇪 A", "DE"}), Skipped: 3, Duplicates: 1}, nil
	}
	res, err = svc.SyncSource(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, database.SourceSyncPartial, res.Status)
	assert.Equal(t, "skipped:3", res.Error)
	assert.Equal(t, 3, res.Skipped)
	assert.Equal(t, 1, res.Duplicates)
	assert.Equal(t, 1, res.Total)
}

func TestBuilderService_SourceValidation(t *testing.T) {
	t.Parallel()
	svc, f, db := newCatalogueService(t)
	f.next = func() (*FetchedCatalogue, error) { return nil, codedErr("unreachable") }

	for _, tc := range []struct {
		name  string
		edit  func(*CreateSourceInput)
		field string
	}{
		{"empty name", func(in *CreateSourceInput) { in.Name = "  " }, "name"},
		{"free-text type", func(in *CreateSourceInput) { in.Type = "xui" }, "type"},
		{"not http", func(in *CreateSourceInput) { in.SubscriptionURL = "ftp://provider.example/sub" }, "subscription_url"},
		{"no host", func(in *CreateSourceInput) { in.SubscriptionURL = "https:///sub" }, "subscription_url"},
		{"fragment", func(in *CreateSourceInput) { in.SubscriptionURL = "https://provider.example/sub#x" }, "subscription_url"},
		{"headers not an object", func(in *CreateSourceInput) { in.Headers = `["x"]` }, "headers"},
		{"bad header name", func(in *CreateSourceInput) { in.Headers = `{"bad name":"v"}` }, "headers"},
		{"control char in hwid", func(in *CreateSourceInput) { in.HWID = "a\nb" }, "hwid"},
		{"control char in user agent", func(in *CreateSourceInput) { in.UserAgent = "a\x01" }, "user_agent"},
	} {
		in := validSourceInput("v-" + tc.field)
		tc.edit(&in)
		_, _, err := svc.CreateSource(context.Background(), in)
		var fe *SourceFieldError
		require.ErrorAs(t, err, &fe, tc.name)
		assert.Equal(t, tc.field, fe.Field, tc.name)
		assert.ErrorIs(t, err, database.ErrProviderSourceInvalid, tc.name)
	}
	sources, err := db.ListProviderSources(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sources, "invalid input never creates a source")
	assert.Zero(t, f.calls)

	// Every documented format is accepted; the source is created even when
	// the initial sync fails.
	for _, format := range database.SourceFormats {
		in := validSourceInput("fmt-" + format)
		in.Type = format
		view, sync, err := svc.CreateSource(context.Background(), in)
		require.NoError(t, err, format)
		assert.Equal(t, format, view.Type)
		require.NotNil(t, sync)
		assert.Equal(t, database.SourceSyncError, sync.Status)
	}

	// Update validates the type too and keeps the stored credentials.
	views, err := svc.ListSources(context.Background())
	require.NoError(t, err)
	_, err = svc.UpdateSource(context.Background(), views[0].ID, UpdateSourceInput{Name: "renamed", Type: "relayhub"})
	var fe *SourceFieldError
	require.ErrorAs(t, err, &fe)
	assert.Equal(t, "type", fe.Field)
	updated, err := svc.UpdateSource(context.Background(), views[0].ID, UpdateSourceInput{Name: "renamed", Type: "clash"})
	require.NoError(t, err)
	assert.Equal(t, "clash", updated.Type)
	stored, err := db.GetProviderSourceByID(context.Background(), views[0].ID)
	require.NoError(t, err)
	assert.NotEmpty(t, stored.SubscriptionURL)
}

func TestBuilderService_SyncInProgressAndNoFetcher(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, f, db := newCatalogueService(t)
	f.next = func() (*FetchedCatalogue, error) {
		return &FetchedCatalogue{Format: "plain", Entries: entriesOf([3]string{"a", "A", ""})}, nil
	}
	view, _, err := svc.CreateSource(ctx, validSourceInput("busy"))
	require.NoError(t, err)

	gate := make(chan struct{})
	f.mu.Lock()
	f.gate = gate
	f.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := svc.SyncSource(ctx, view.ID); done <- err }()
	require.Eventually(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.calls == 2 }, 2*time.Second, 5*time.Millisecond)

	_, err = svc.SyncSource(ctx, view.ID)
	assert.ErrorIs(t, err, database.ErrProviderSourceSyncInProgress, "one sync per source at a time")
	close(gate)
	require.NoError(t, <-done)

	_, err = svc.SyncSource(ctx, 999999)
	assert.ErrorIs(t, err, database.ErrProviderSourceNotFound)

	bare := NewBuilderService(db)
	_, err = bare.SyncSource(ctx, view.ID)
	assert.ErrorIs(t, err, ErrCatalogueFetcherUnavailable)
}
