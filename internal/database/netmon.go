package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Network monitor storage (migration 048). The monitor writes here from its
// background worker only; the admin API reads. Nothing in these tables holds
// a credential: endpoints are protocol/host/port/transport, bindings hold the
// credential-free catalogue fingerprint.

// MonitorTarget is one checked network endpoint (monitor_targets).
type MonitorTarget struct {
	ID                  uint       `gorm:"primaryKey;column:id" json:"id"`
	EndpointKey         string     `gorm:"column:endpoint_key" json:"-"`
	Protocol            string     `gorm:"column:protocol" json:"protocol"`
	Host                string     `gorm:"column:host" json:"host"`
	Port                int        `gorm:"column:port" json:"port"`
	Security            string     `gorm:"column:security" json:"security"`
	Transport           string     `gorm:"column:transport" json:"transport"`
	SNI                 string     `gorm:"column:sni" json:"sni"`
	Probe               string     `gorm:"column:probe" json:"probe"`
	Active              bool       `gorm:"column:active" json:"active"`
	Status              string     `gorm:"column:status" json:"status"`
	StatusSince         *time.Time `gorm:"column:status_since" json:"status_since"`
	ConsecutiveFailures int        `gorm:"column:consecutive_failures" json:"consecutive_failures"`
	FirstFailureAt      *time.Time `gorm:"column:first_failure_at" json:"-"`
	LastCheckedAt       *time.Time `gorm:"column:last_checked_at" json:"last_checked_at"`
	LastUpAt            *time.Time `gorm:"column:last_up_at" json:"last_up_at"`
	LastDownAt          *time.Time `gorm:"column:last_down_at" json:"last_down_at"`
	LastLatencyMS       *int       `gorm:"column:last_latency_ms" json:"last_latency_ms"`
	LastError           string     `gorm:"column:last_error" json:"last_error"`
	FirstSeenAt         time.Time  `gorm:"column:first_seen_at" json:"first_seen_at"`
	LastSeenAt          time.Time  `gorm:"column:last_seen_at" json:"last_seen_at"`
}

// TableName pins the table.
func (MonitorTarget) TableName() string { return "monitor_targets" }

// MonitorBinding says a builder serves a catalogue server on a target.
type MonitorBinding struct {
	ID          uint      `gorm:"primaryKey;column:id"`
	BuilderID   uint      `gorm:"column:builder_id"`
	BuilderName string    `gorm:"column:builder_name"`
	SourceID    uint      `gorm:"column:source_id"`
	Fingerprint string    `gorm:"column:fingerprint"`
	TargetID    uint      `gorm:"column:target_id"`
	CountryCode string    `gorm:"column:country_code"`
	NodeName    string    `gorm:"column:node_name"`
	Active      bool      `gorm:"column:active"`
	FirstSeenAt time.Time `gorm:"column:first_seen_at"`
	LastSeenAt  time.Time `gorm:"column:last_seen_at"`
}

// TableName pins the table.
func (MonitorBinding) TableName() string { return "monitor_bindings" }

// MonitorCountryState is the current state of one builder country.
type MonitorCountryState struct {
	BuilderID     uint       `gorm:"primaryKey;column:builder_id"`
	CountryCode   string     `gorm:"primaryKey;column:country_code"`
	Status        string     `gorm:"column:status"`
	StatusSince   *time.Time `gorm:"column:status_since"`
	Active        bool       `gorm:"column:active"`
	LastCheckedAt *time.Time `gorm:"column:last_checked_at"`
	LastUpAt      *time.Time `gorm:"column:last_up_at"`
	LastDownAt    *time.Time `gorm:"column:last_down_at"`
}

// TableName pins the table.
func (MonitorCountryState) TableName() string { return "monitor_country_states" }

// MonitorOutage is one outage of a target or a builder country.
type MonitorOutage struct {
	ID          uint       `gorm:"primaryKey;column:id"`
	Scope       string     `gorm:"column:scope"`
	TargetID    *uint      `gorm:"column:target_id"`
	BuilderID   *uint      `gorm:"column:builder_id"`
	CountryCode string     `gorm:"column:country_code"`
	Kind        string     `gorm:"column:kind"`
	StartedAt   time.Time  `gorm:"column:started_at"`
	EndedAt     *time.Time `gorm:"column:ended_at"`
	EndReason   string     `gorm:"column:end_reason"`
	ErrorCode   string     `gorm:"column:error_code"`
}

// TableName pins the table.
func (MonitorOutage) TableName() string { return "monitor_outages" }

// MonitorEndpointSnapshot is a resolved credential-free endpoint.
type MonitorEndpointSnapshot struct {
	Key, Protocol, Host             string
	Port                            int
	Security, Transport, SNI, Probe string
}

// MonitorNodeSnapshot is one server a builder currently serves.
type MonitorNodeSnapshot struct {
	SourceID    uint
	Fingerprint string
	Name        string
	Country     string
	Endpoint    MonitorEndpointSnapshot
}

// MonitorBuilderSnapshot is the resolved state of one enabled builder.
type MonitorBuilderSnapshot struct {
	BuilderID   uint
	BuilderName string
	// ScopeSources: sources the builder fetches now. Bindings of any other
	// source are no longer served (disabled, unlinked, no rule uses it).
	ScopeSources []uint
	// FetchedSources: scope sources fetched in this run. A binding of a
	// scope source that failed to fetch is kept: unknown is not gone.
	FetchedSources []uint
	Nodes          []MonitorNodeSnapshot
}

// ListMonitorBuilders returns every builder (enabled or not) with its sources
// (source preloaded) and items in runtime order — the same shape
// ResolveSubscriptionBuilder serves /sub with.
func (s *Service) ListMonitorBuilders(ctx context.Context) ([]SubscriptionBuilder, error) {
	var builders []SubscriptionBuilder
	err := s.db.WithContext(ctx).
		Preload("Sources", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC, source_id ASC") }).
		Preload("Sources.Source").
		Preload("Items", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC, id ASC") }).
		Order("name ASC, id ASC").
		Find(&builders).Error
	if err != nil {
		return nil, fmt.Errorf("list monitor builders: %w", err)
	}
	return builders, nil
}

// SyncMonitorBindings stores the current resolution of the enabled builders:
// upserts targets and bindings, deactivates bindings that are no longer
// served, and deactivates targets and countries nothing serves any more.
// Deactivation closes an open outage with end_reason "removed" and resets the
// state so a later reappearance starts clean; no row is ever deleted.
func (s *Service) SyncMonitorBindings(ctx context.Context, now time.Time, snapshots []MonitorBuilderSnapshot) error {
	now = now.UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		targetIDs := make(map[string]uint)
		seenBindings := make(map[uint]bool)
		inSnapshot := make(map[uint]*MonitorBuilderSnapshot, len(snapshots))
		for i := range snapshots {
			snap := &snapshots[i]
			inSnapshot[snap.BuilderID] = snap
			for _, n := range snap.Nodes {
				id, ok := targetIDs[n.Endpoint.Key]
				if !ok {
					var err error
					id, err = upsertMonitorTarget(tx, now, n.Endpoint)
					if err != nil {
						return err
					}
					targetIDs[n.Endpoint.Key] = id
				}
				bindingID, err := upsertMonitorBinding(tx, now, snap, n, id)
				if err != nil {
					return err
				}
				seenBindings[bindingID] = true
			}
		}

		var active []MonitorBinding
		if err := tx.Where("active = ?", true).Find(&active).Error; err != nil {
			return fmt.Errorf("load active monitor bindings: %w", err)
		}
		var retire []uint
		for _, b := range active {
			if seenBindings[b.ID] {
				continue
			}
			snap := inSnapshot[b.BuilderID]
			switch {
			case snap == nil: // builder disabled or deleted
				retire = append(retire, b.ID)
			case !containsUint(snap.ScopeSources, b.SourceID): // source disabled / unlinked
				retire = append(retire, b.ID)
			case containsUint(snap.FetchedSources, b.SourceID): // fetched, server gone or filtered out
				retire = append(retire, b.ID)
			}
		}
		if len(retire) > 0 {
			if err := tx.Model(&MonitorBinding{}).Where("id IN ?", retire).Update("active", false).Error; err != nil {
				return fmt.Errorf("retire monitor bindings: %w", err)
			}
		}

		if err := retireUnservedTargets(tx, now); err != nil {
			return err
		}
		return syncCountryStateRows(tx, now)
	})
}

func upsertMonitorTarget(tx *gorm.DB, now time.Time, ep MonitorEndpointSnapshot) (uint, error) {
	var t MonitorTarget
	err := tx.Where("endpoint_key = ?", ep.Key).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		t = MonitorTarget{
			EndpointKey: ep.Key, Protocol: ep.Protocol, Host: ep.Host, Port: ep.Port, Security: ep.Security,
			Transport: ep.Transport, SNI: ep.SNI, Probe: ep.Probe, Active: true, Status: MonitorStatusUnknown,
			FirstSeenAt: now, LastSeenAt: now,
		}
		if err := tx.Create(&t).Error; err != nil {
			return 0, fmt.Errorf("create monitor target: %w", err)
		}
		return t.ID, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load monitor target: %w", err)
	}
	if err := tx.Model(&MonitorTarget{}).Where("id = ?", t.ID).Updates(map[string]any{
		"active": true, "last_seen_at": now, "protocol": ep.Protocol, "host": ep.Host, "port": ep.Port,
		"security": ep.Security, "transport": ep.Transport, "sni": ep.SNI, "probe": ep.Probe,
	}).Error; err != nil {
		return 0, fmt.Errorf("update monitor target: %w", err)
	}
	return t.ID, nil
}

func upsertMonitorBinding(tx *gorm.DB, now time.Time, snap *MonitorBuilderSnapshot, n MonitorNodeSnapshot, targetID uint) (uint, error) {
	var b MonitorBinding
	err := tx.Where("builder_id = ? AND source_id = ? AND fingerprint = ?", snap.BuilderID, n.SourceID, n.Fingerprint).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		b = MonitorBinding{
			BuilderID: snap.BuilderID, BuilderName: snap.BuilderName, SourceID: n.SourceID, Fingerprint: n.Fingerprint,
			TargetID: targetID, CountryCode: n.Country, NodeName: n.Name, Active: true, FirstSeenAt: now, LastSeenAt: now,
		}
		if err := tx.Create(&b).Error; err != nil {
			return 0, fmt.Errorf("create monitor binding: %w", err)
		}
		return b.ID, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load monitor binding: %w", err)
	}
	if err := tx.Model(&MonitorBinding{}).Where("id = ?", b.ID).Updates(map[string]any{
		"active": true, "last_seen_at": now, "target_id": targetID, "country_code": n.Country,
		"node_name": n.Name, "builder_name": snap.BuilderName,
	}).Error; err != nil {
		return 0, fmt.Errorf("update monitor binding: %w", err)
	}
	return b.ID, nil
}

// retireUnservedTargets deactivates active targets without an active binding.
func retireUnservedTargets(tx *gorm.DB, now time.Time) error {
	var ids []uint
	if err := tx.Model(&MonitorTarget{}).
		Where("active = ? AND id NOT IN (SELECT target_id FROM monitor_bindings WHERE active = ?)", true, true).
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("find unserved monitor targets: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Model(&MonitorOutage{}).
		Where("scope = ? AND target_id IN ? AND ended_at IS NULL", MonitorScopeTarget, ids).
		Updates(map[string]any{"ended_at": now, "end_reason": MonitorEndRemoved}).Error; err != nil {
		return fmt.Errorf("close outages of unserved targets: %w", err)
	}
	if err := tx.Model(&MonitorTarget{}).Where("id IN ?", ids).Updates(map[string]any{
		"active": false, "status": MonitorStatusUnknown, "status_since": now,
		"consecutive_failures": 0, "first_failure_at": nil,
	}).Error; err != nil {
		return fmt.Errorf("retire monitor targets: %w", err)
	}
	return nil
}

// syncCountryStateRows makes the active country states match the countries
// of active bindings: new countries get an unknown row, countries nothing
// serves any more are deactivated (open outage closed as removed).
func syncCountryStateRows(tx *gorm.DB, now time.Time) error {
	type key struct {
		BuilderID   uint
		CountryCode string
	}
	var served []key
	if err := tx.Model(&MonitorBinding{}).Where("active = ?", true).
		Distinct("builder_id", "country_code").Scan(&served).Error; err != nil {
		return fmt.Errorf("list served monitor countries: %w", err)
	}
	isServed := make(map[key]bool, len(served))
	for _, k := range served {
		isServed[k] = true
	}

	var states []MonitorCountryState
	if err := tx.Find(&states).Error; err != nil {
		return fmt.Errorf("load monitor country states: %w", err)
	}
	known := make(map[key]MonitorCountryState, len(states))
	for _, st := range states {
		k := key{st.BuilderID, st.CountryCode}
		known[k] = st
		if st.Active && !isServed[k] {
			if err := closeCountryOutage(tx, st.BuilderID, st.CountryCode, now, MonitorEndRemoved); err != nil {
				return err
			}
			if err := tx.Model(&MonitorCountryState{}).
				Where("builder_id = ? AND country_code = ?", st.BuilderID, st.CountryCode).
				Updates(map[string]any{"active": false, "status": MonitorStatusUnknown, "status_since": now}).Error; err != nil {
				return fmt.Errorf("retire monitor country: %w", err)
			}
		}
	}
	for _, k := range served {
		st, ok := known[k]
		switch {
		case !ok:
			row := MonitorCountryState{BuilderID: k.BuilderID, CountryCode: k.CountryCode, Status: MonitorStatusUnknown, StatusSince: &now, Active: true}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create monitor country: %w", err)
			}
		case !st.Active:
			if err := tx.Model(&MonitorCountryState{}).
				Where("builder_id = ? AND country_code = ?", k.BuilderID, k.CountryCode).
				Updates(map[string]any{"active": true, "status": MonitorStatusUnknown, "status_since": now}).Error; err != nil {
				return fmt.Errorf("reactivate monitor country: %w", err)
			}
		}
	}
	return nil
}

func closeCountryOutage(tx *gorm.DB, builderID uint, country string, at time.Time, reason string) error {
	if err := tx.Model(&MonitorOutage{}).
		Where("scope = ? AND builder_id = ? AND country_code = ? AND ended_at IS NULL", MonitorScopeCountry, builderID, country).
		Updates(map[string]any{"ended_at": at, "end_reason": reason}).Error; err != nil {
		return fmt.Errorf("close country outage: %w", err)
	}
	return nil
}

// ListActiveMonitorTargets returns the targets to check, by id.
func (s *Service) ListActiveMonitorTargets(ctx context.Context) ([]MonitorTarget, error) {
	var targets []MonitorTarget
	if err := s.db.WithContext(ctx).Where("active = ?", true).Order("id ASC").Find(&targets).Error; err != nil {
		return nil, fmt.Errorf("list active monitor targets: %w", err)
	}
	return targets, nil
}

// RecordMonitorChecks applies one check round in a single transaction: target
// states and outages (applyTargetCheck), 5-minute samples, then the country
// states of every builder (MonitorCountryStatusOf / countryTransition).
// Results for inactive or unknown targets are ignored.
func (s *Service) RecordMonitorChecks(ctx context.Context, now time.Time, results []MonitorCheckResult, downAfter int) error {
	now = now.UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids := make([]uint, 0, len(results))
		for _, r := range results {
			ids = append(ids, r.TargetID)
		}
		var targets []MonitorTarget
		if len(ids) > 0 {
			if err := tx.Where("id IN ? AND active = ?", ids, true).Find(&targets).Error; err != nil {
				return fmt.Errorf("load monitor targets: %w", err)
			}
		}
		byID := make(map[uint]*MonitorTarget, len(targets))
		for i := range targets {
			byID[targets[i].ID] = &targets[i]
		}

		for _, r := range results {
			t := byID[r.TargetID]
			if t == nil {
				continue
			}
			tr := applyTargetCheck(t, r, downAfter)
			if err := tx.Model(&MonitorTarget{}).Where("id = ?", t.ID).Updates(map[string]any{
				"status": t.Status, "status_since": t.StatusSince, "consecutive_failures": t.ConsecutiveFailures,
				"first_failure_at": t.FirstFailureAt, "last_checked_at": t.LastCheckedAt, "last_up_at": t.LastUpAt,
				"last_down_at": t.LastDownAt, "last_latency_ms": t.LastLatencyMS, "last_error": t.LastError,
			}).Error; err != nil {
				return fmt.Errorf("update monitor target: %w", err)
			}
			if tr.close {
				if err := tx.Model(&MonitorOutage{}).
					Where("scope = ? AND target_id = ? AND ended_at IS NULL", MonitorScopeTarget, t.ID).
					Updates(map[string]any{"ended_at": tr.closeAt, "end_reason": MonitorEndRecovered}).Error; err != nil {
					return fmt.Errorf("close target outage: %w", err)
				}
			}
			if tr.open {
				id := t.ID
				o := MonitorOutage{Scope: MonitorScopeTarget, TargetID: &id, Kind: MonitorOutageDown, StartedAt: tr.openAt, ErrorCode: tr.errorCode}
				if err := tx.Create(&o).Error; err != nil {
					return fmt.Errorf("open target outage: %w", err)
				}
			}
			if err := addMonitorSample(tx, r); err != nil {
				return err
			}
		}

		return applyCountryStates(tx, now)
	})
}

func addMonitorSample(tx *gorm.DB, r MonitorCheckResult) error {
	failures, count := 1, 0
	var latency *int
	if r.OK {
		failures, count = 0, 1
		l := r.LatencyMS
		latency = &l
	}
	sum := 0
	if latency != nil {
		sum = *latency
	}
	err := tx.Exec(`INSERT INTO monitor_samples
		(target_id, bucket_start, checks, failures, latency_count, latency_sum_ms, latency_min_ms, latency_max_ms)
		VALUES (?, ?, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(target_id, bucket_start) DO UPDATE SET
			checks = checks + 1,
			failures = failures + excluded.failures,
			latency_count = latency_count + excluded.latency_count,
			latency_sum_ms = latency_sum_ms + excluded.latency_sum_ms,
			latency_min_ms = CASE WHEN excluded.latency_min_ms IS NULL THEN latency_min_ms
				WHEN latency_min_ms IS NULL OR excluded.latency_min_ms < latency_min_ms THEN excluded.latency_min_ms
				ELSE latency_min_ms END,
			latency_max_ms = CASE WHEN excluded.latency_max_ms IS NULL THEN latency_max_ms
				WHEN latency_max_ms IS NULL OR excluded.latency_max_ms > latency_max_ms THEN excluded.latency_max_ms
				ELSE latency_max_ms END`,
		r.TargetID, monitorBucketOf(r.CheckedAt), failures, count, sum, latency, latency).Error
	if err != nil {
		return fmt.Errorf("record monitor sample: %w", err)
	}
	return nil
}

// applyCountryStates recomputes every active builder country from its active
// bindings and applies the outage transitions.
func applyCountryStates(tx *gorm.DB, now time.Time) error {
	type row struct {
		BuilderID   uint
		CountryCode string
		Status      string
		LastError   string
	}
	var rows []row
	if err := tx.Raw(`SELECT b.builder_id, b.country_code, t.status, t.last_error
		FROM monitor_bindings b JOIN monitor_targets t ON t.id = b.target_id
		WHERE b.active = 1 AND t.active = 1`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load monitor country members: %w", err)
	}
	type key struct {
		builder uint
		country string
	}
	statuses := make(map[key][]string)
	errorsOf := make(map[key][]string)
	for _, r := range rows {
		k := key{r.BuilderID, r.CountryCode}
		statuses[k] = append(statuses[k], r.Status)
		if r.Status == MonitorStatusDown {
			errorsOf[k] = append(errorsOf[k], r.LastError)
		}
	}

	var states []MonitorCountryState
	if err := tx.Where("active = ?", true).Find(&states).Error; err != nil {
		return fmt.Errorf("load monitor country states: %w", err)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].BuilderID != states[j].BuilderID {
			return states[i].BuilderID < states[j].BuilderID
		}
		return states[i].CountryCode < states[j].CountryCode
	})
	for _, st := range states {
		k := key{st.BuilderID, st.CountryCode}
		next := MonitorCountryStatusOf(statuses[k])
		updates := map[string]any{"last_checked_at": now}
		switch next {
		case MonitorStatusUp:
			updates["last_up_at"] = now
		case MonitorStatusDown:
			if st.Status != MonitorStatusDown {
				updates["last_down_at"] = now
			}
		}
		closeReason, openKind := countryTransition(st.Status, next)
		if closeReason != "" {
			if err := closeCountryOutage(tx, st.BuilderID, st.CountryCode, now, closeReason); err != nil {
				return err
			}
		}
		if openKind != "" {
			builderID := st.BuilderID
			o := MonitorOutage{
				Scope: MonitorScopeCountry, BuilderID: &builderID, CountryCode: st.CountryCode, Kind: openKind,
				StartedAt: now, ErrorCode: dominantError(errorsOf[k]),
			}
			if err := tx.Create(&o).Error; err != nil {
				return fmt.Errorf("open country outage: %w", err)
			}
		}
		if next != MonitorStatusUnknown && next != st.Status {
			updates["status"] = next
			updates["status_since"] = now
		}
		if err := tx.Model(&MonitorCountryState{}).
			Where("builder_id = ? AND country_code = ?", st.BuilderID, st.CountryCode).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("update monitor country: %w", err)
		}
	}
	return nil
}

// PruneMonitorSamples deletes 5-minute samples older than cutoff. Outages,
// bindings and targets are kept forever.
func (s *Service) PruneMonitorSamples(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Exec("DELETE FROM monitor_samples WHERE bucket_start < ?", cutoff.UTC().Unix())
	if res.Error != nil {
		return 0, fmt.Errorf("prune monitor samples: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ---------------------------------------------------------------------------
// Read side (admin API)
// ---------------------------------------------------------------------------

// MonitorBindingRow is a binding with its target (current state).
type MonitorBindingRow struct {
	MonitorBinding
	Target MonitorTarget `gorm:"-"`
}

// ListMonitorBindings returns bindings (optionally of one builder), with
// their targets, ordered by builder, country and name.
func (s *Service) ListMonitorBindings(ctx context.Context, builderID uint, activeOnly bool) ([]MonitorBindingRow, error) {
	q := s.db.WithContext(ctx).Model(&MonitorBinding{})
	if builderID != 0 {
		q = q.Where("builder_id = ?", builderID)
	}
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	var bindings []MonitorBinding
	if err := q.Order("builder_id ASC, country_code ASC, node_name ASC, id ASC").Find(&bindings).Error; err != nil {
		return nil, fmt.Errorf("list monitor bindings: %w", err)
	}
	ids := make([]uint, 0, len(bindings))
	for _, b := range bindings {
		ids = append(ids, b.TargetID)
	}
	targets := make(map[uint]MonitorTarget)
	if len(ids) > 0 {
		var ts []MonitorTarget
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&ts).Error; err != nil {
			return nil, fmt.Errorf("load monitor binding targets: %w", err)
		}
		for _, t := range ts {
			targets[t.ID] = t
		}
	}
	out := make([]MonitorBindingRow, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, MonitorBindingRow{MonitorBinding: b, Target: targets[b.TargetID]})
	}
	return out, nil
}

// ListMonitorCountryStates returns country states (optionally of one builder).
func (s *Service) ListMonitorCountryStates(ctx context.Context, builderID uint) ([]MonitorCountryState, error) {
	q := s.db.WithContext(ctx).Model(&MonitorCountryState{})
	if builderID != 0 {
		q = q.Where("builder_id = ?", builderID)
	}
	var states []MonitorCountryState
	if err := q.Order("builder_id ASC, country_code ASC").Find(&states).Error; err != nil {
		return nil, fmt.Errorf("list monitor country states: %w", err)
	}
	return states, nil
}

// MonitorOutageFilter selects outages overlapping [From, To).
type MonitorOutageFilter struct {
	Scope     string
	BuilderID uint
	Country   *string
	TargetIDs []uint
	From, To  time.Time
	Limit     int
}

// ListMonitorOutages returns outages overlapping the period, newest first.
func (s *Service) ListMonitorOutages(ctx context.Context, f MonitorOutageFilter) ([]MonitorOutage, error) {
	q := s.db.WithContext(ctx).Model(&MonitorOutage{}).
		Where("started_at < ? AND (ended_at IS NULL OR ended_at >= ?)", f.To.UTC(), f.From.UTC())
	if f.Scope != "" {
		q = q.Where("scope = ?", f.Scope)
	}
	if f.BuilderID != 0 {
		q = q.Where("builder_id = ?", f.BuilderID)
	}
	if f.Country != nil {
		q = q.Where("country_code = ?", *f.Country)
	}
	if f.TargetIDs != nil {
		if len(f.TargetIDs) == 0 {
			return nil, nil
		}
		q = q.Where("target_id IN ?", f.TargetIDs)
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	var out []MonitorOutage
	if err := q.Order("started_at DESC, id DESC").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("list monitor outages: %w", err)
	}
	return out, nil
}

// LatestMonitorCountryOutages returns the most recent country outage of each
// builder (for "last transition").
func (s *Service) LatestMonitorCountryOutages(ctx context.Context) ([]MonitorOutage, error) {
	var out []MonitorOutage
	err := s.db.WithContext(ctx).Raw(`SELECT o.* FROM monitor_outages o
		WHERE o.scope = ? AND o.id = (
			SELECT o2.id FROM monitor_outages o2 WHERE o2.scope = ? AND o2.builder_id = o.builder_id
			ORDER BY COALESCE(o2.ended_at, o2.started_at) DESC, o2.id DESC LIMIT 1)`,
		MonitorScopeCountry, MonitorScopeCountry).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("latest monitor country outages: %w", err)
	}
	return out, nil
}

// MonitorLatencyPoint aggregates samples of a set of targets over one step.
type MonitorLatencyPoint struct {
	BucketStart  int64 `json:"bucket_start"`
	Checks       int   `json:"checks"`
	Failures     int   `json:"failures"`
	LatencyCount int   `json:"-"`
	LatencySum   int64 `json:"-"`
	LatencyMin   *int  `json:"latency_min_ms"`
	LatencyMax   *int  `json:"latency_max_ms"`
}

// MonitorLatencySeries returns sample aggregates of the targets from `from`,
// grouped into buckets of stepSeconds (a multiple of MonitorSampleBucket).
func (s *Service) MonitorLatencySeries(ctx context.Context, targetIDs []uint, from time.Time, stepSeconds int64) ([]MonitorLatencyPoint, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	if stepSeconds < MonitorSampleBucket {
		stepSeconds = MonitorSampleBucket
	}
	var out []MonitorLatencyPoint
	err := s.db.WithContext(ctx).Raw(`SELECT (bucket_start / ?) * ? AS bucket_start,
			SUM(checks) AS checks, SUM(failures) AS failures,
			SUM(latency_count) AS latency_count, SUM(latency_sum_ms) AS latency_sum,
			MIN(latency_min_ms) AS latency_min, MAX(latency_max_ms) AS latency_max
		FROM monitor_samples WHERE target_id IN ? AND bucket_start >= ?
		GROUP BY 1 ORDER BY 1`, stepSeconds, stepSeconds, targetIDs, from.UTC().Unix()).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("monitor latency series: %w", err)
	}
	return out, nil
}

// ListMonitorTargets returns targets by id.
func (s *Service) ListMonitorTargets(ctx context.Context, ids []uint) ([]MonitorTarget, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []MonitorTarget
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, fmt.Errorf("list monitor targets: %w", err)
	}
	return out, nil
}

func containsUint(list []uint, v uint) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
