package database

import (
	"sort"
	"time"
)

// Network monitor state machine (migration 048). Pure functions: the same
// inputs always produce the same state and the same outage transition, so
// UP/DOWN/DEGRADED and outage bookkeeping are deterministic and testable
// without a network.

// Monitor statuses. Targets use unknown/up/down; builder countries add
// degraded (some of the country's servers answer, some do not).
const (
	MonitorStatusUnknown  = "unknown"
	MonitorStatusUp       = "up"
	MonitorStatusDown     = "down"
	MonitorStatusDegraded = "degraded"
)

// Outage scopes, kinds and end reasons.
const (
	MonitorScopeTarget  = "target"
	MonitorScopeCountry = "country"

	MonitorOutageDown     = "down"
	MonitorOutageDegraded = "degraded"

	// MonitorEndRecovered: the subject is UP again.
	MonitorEndRecovered = "recovered"
	// MonitorEndChanged: a country outage turned into another kind
	// (degraded <-> down); the new kind opens its own outage.
	MonitorEndChanged = "changed"
	// MonitorEndRemoved: the subject is no longer served by any builder
	// (catalogue or configuration change); it did not recover.
	MonitorEndRemoved = "removed"
)

// DefaultMonitorDownAfter is the number of consecutive failed checks after
// which a target is DOWN. One failure of an UP target is tolerated so a single
// lost packet does not open an outage.
const DefaultMonitorDownAfter = 2

// MonitorCheckResult is the outcome of one handshake-level check.
type MonitorCheckResult struct {
	TargetID  uint
	OK        bool
	LatencyMS int
	ErrorCode string
	CheckedAt time.Time
}

// targetTransition says which outage bookkeeping a check requires.
type targetTransition struct {
	open      bool
	openAt    time.Time
	close     bool
	closeAt   time.Time
	errorCode string
}

// applyTargetCheck advances a target by one check result:
//
//	success:           status UP; a DOWN target closes its outage (recovered)
//	failure, UP/unknown: counts consecutive failures; at downAfter the target
//	                   is DOWN and an outage opens at the FIRST failed check
//	failure, DOWN:     nothing new (no duplicate outage)
func applyTargetCheck(t *MonitorTarget, r MonitorCheckResult, downAfter int) targetTransition {
	if downAfter < 1 {
		downAfter = 1
	}
	at := r.CheckedAt.UTC()
	t.LastCheckedAt = &at

	var tr targetTransition
	if r.OK {
		latency := r.LatencyMS
		t.LastLatencyMS = &latency
		t.LastUpAt = &at
		t.LastError = ""
		t.ConsecutiveFailures = 0
		t.FirstFailureAt = nil
		if t.Status != MonitorStatusUp {
			if t.Status == MonitorStatusDown {
				tr.close, tr.closeAt = true, at
			}
			t.Status = MonitorStatusUp
			t.StatusSince = &at
		}
		return tr
	}

	t.LastError = r.ErrorCode
	t.LastLatencyMS = nil
	if t.ConsecutiveFailures == 0 || t.FirstFailureAt == nil {
		t.FirstFailureAt = &at
	}
	t.ConsecutiveFailures++
	if t.Status != MonitorStatusDown && t.ConsecutiveFailures >= downAfter {
		since := *t.FirstFailureAt
		t.Status = MonitorStatusDown
		t.StatusSince = &since
		t.LastDownAt = &since
		tr.open, tr.openAt, tr.errorCode = true, since, r.ErrorCode
	}
	return tr
}

// MonitorCountryStatusOf derives a country status from its servers:
//
//	UP       every checked server answers
//	DEGRADED some servers answer, some are DOWN
//	DOWN     no server answers
//	unknown  no server has a verdict yet (not checked, or unsupported)
func MonitorCountryStatusOf(targetStatuses []string) string {
	up, down := 0, 0
	for _, s := range targetStatuses {
		switch s {
		case MonitorStatusUp:
			up++
		case MonitorStatusDown:
			down++
		}
	}
	switch {
	case up == 0 && down == 0:
		return MonitorStatusUnknown
	case down == 0:
		return MonitorStatusUp
	case up == 0:
		return MonitorStatusDown
	default:
		return MonitorStatusDegraded
	}
}

// MonitorAggregateStatus derives a builder status from its countries with the
// same rule (a country counts as up/down; degraded makes the builder degraded).
func MonitorAggregateStatus(countryStatuses []string) string {
	up, down, degraded := 0, 0, 0
	for _, s := range countryStatuses {
		switch s {
		case MonitorStatusUp:
			up++
		case MonitorStatusDown:
			down++
		case MonitorStatusDegraded:
			degraded++
		}
	}
	switch {
	case up == 0 && down == 0 && degraded == 0:
		return MonitorStatusUnknown
	case down == 0 && degraded == 0:
		return MonitorStatusUp
	case up == 0 && degraded == 0:
		return MonitorStatusDown
	default:
		return MonitorStatusDegraded
	}
}

// countryTransition says how a country outage changes between two statuses.
// unknown (no verdict) never changes the state or its outage.
func countryTransition(prev, next string) (closeReason string, openKind string) {
	if next == MonitorStatusUnknown || prev == next {
		return "", ""
	}
	if prev == MonitorStatusDown || prev == MonitorStatusDegraded {
		closeReason = MonitorEndChanged
		if next == MonitorStatusUp {
			closeReason = MonitorEndRecovered
		}
	}
	if next == MonitorStatusDown || next == MonitorStatusDegraded {
		openKind = next
	}
	return closeReason, openKind
}

// dominantError returns the most frequent non-empty code (ties: smallest).
func dominantError(codes []string) string {
	counts := make(map[string]int)
	for _, c := range codes {
		if c != "" {
			counts[c]++
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best, bestN := "", 0
	for _, k := range keys {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

// MonitorSampleBucket is the width of one monitor_samples row (seconds).
const MonitorSampleBucket = 300

func monitorBucketOf(t time.Time) int64 {
	u := t.UTC().Unix()
	return u - u%MonitorSampleBucket
}
