package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var monT0 = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

func monAt(minutes int) time.Time { return monT0.Add(time.Duration(minutes) * time.Minute) }

func okCheck(minute, latency int) MonitorCheckResult {
	return MonitorCheckResult{TargetID: 1, OK: true, LatencyMS: latency, CheckedAt: monAt(minute)}
}

func failCheck(minute int, code string) MonitorCheckResult {
	return MonitorCheckResult{TargetID: 1, ErrorCode: code, CheckedAt: monAt(minute)}
}

func TestApplyTargetCheck_Transitions(t *testing.T) {
	t.Parallel()

	target := &MonitorTarget{Status: MonitorStatusUnknown}

	// unknown -> UP: no outage bookkeeping.
	tr := applyTargetCheck(target, okCheck(0, 40), 2)
	assert.Equal(t, targetTransition{}, tr)
	assert.Equal(t, MonitorStatusUp, target.Status)
	assert.Equal(t, monAt(0), *target.StatusSince)
	assert.Equal(t, 40, *target.LastLatencyMS)

	// UP -> UP: nothing changes but the check times and latency.
	tr = applyTargetCheck(target, okCheck(1, 55), 2)
	assert.Equal(t, targetTransition{}, tr)
	assert.Equal(t, monAt(0), *target.StatusSince, "UP/UP keeps the status start")
	assert.Equal(t, monAt(1), *target.LastUpAt)
	assert.Equal(t, 55, *target.LastLatencyMS)

	// One failure of an UP target is tolerated.
	tr = applyTargetCheck(target, failCheck(2, "timeout"), 2)
	assert.Equal(t, targetTransition{}, tr)
	assert.Equal(t, MonitorStatusUp, target.Status)
	assert.Equal(t, 1, target.ConsecutiveFailures)
	assert.Nil(t, target.LastLatencyMS)

	// UP -> DOWN at the second failure; the outage starts at the first one.
	tr = applyTargetCheck(target, failCheck(3, "refused"), 2)
	assert.True(t, tr.open)
	assert.Equal(t, monAt(2), tr.openAt)
	assert.Equal(t, "refused", tr.errorCode)
	assert.Equal(t, MonitorStatusDown, target.Status)
	assert.Equal(t, monAt(2), *target.StatusSince)
	assert.Equal(t, monAt(2), *target.LastDownAt)

	// DOWN -> DOWN: no duplicate outage.
	tr = applyTargetCheck(target, failCheck(4, "refused"), 2)
	assert.Equal(t, targetTransition{}, tr)
	assert.Equal(t, 3, target.ConsecutiveFailures)

	// DOWN -> UP closes the outage at the recovering check.
	tr = applyTargetCheck(target, okCheck(5, 30), 2)
	assert.True(t, tr.close)
	assert.Equal(t, monAt(5), tr.closeAt)
	assert.False(t, tr.open)
	assert.Equal(t, MonitorStatusUp, target.Status)
	assert.Equal(t, 0, target.ConsecutiveFailures)
	assert.Nil(t, target.FirstFailureAt)
	assert.Empty(t, target.LastError)
}

func TestApplyTargetCheck_UnknownGoesDownAndThresholdOne(t *testing.T) {
	t.Parallel()

	target := &MonitorTarget{Status: MonitorStatusUnknown}
	assert.Equal(t, targetTransition{}, applyTargetCheck(target, failCheck(0, "dns"), 2))
	assert.Equal(t, MonitorStatusUnknown, target.Status, "one failure is not a verdict yet")
	tr := applyTargetCheck(target, failCheck(1, "dns"), 2)
	assert.True(t, tr.open)
	assert.Equal(t, monAt(0), tr.openAt)

	strict := &MonitorTarget{Status: MonitorStatusUp}
	tr = applyTargetCheck(strict, failCheck(0, "timeout"), 1)
	assert.True(t, tr.open, "down_after=1 opens at the first failure")
	assert.Equal(t, MonitorStatusDown, strict.Status)
}

func TestApplyTargetCheck_Deterministic(t *testing.T) {
	t.Parallel()

	seq := []MonitorCheckResult{okCheck(0, 10), failCheck(1, "timeout"), failCheck(2, "timeout"), failCheck(3, "timeout"), okCheck(4, 12)}
	run := func() ([]targetTransition, MonitorTarget) {
		target := &MonitorTarget{Status: MonitorStatusUnknown}
		var out []targetTransition
		for _, r := range seq {
			out = append(out, applyTargetCheck(target, r, 2))
		}
		return out, *target
	}
	a, ta := run()
	b, tb := run()
	assert.Equal(t, a, b)
	assert.Equal(t, ta, tb)
}

func TestMonitorCountryStatusOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   []string
		want string
	}{
		{nil, MonitorStatusUnknown},
		{[]string{MonitorStatusUnknown, MonitorStatusUnknown}, MonitorStatusUnknown},
		{[]string{MonitorStatusUp, MonitorStatusUp}, MonitorStatusUp},
		{[]string{MonitorStatusUp, MonitorStatusUnknown}, MonitorStatusUp},
		{[]string{MonitorStatusDown, MonitorStatusDown}, MonitorStatusDown},
		{[]string{MonitorStatusDown, MonitorStatusUnknown}, MonitorStatusDown},
		{[]string{MonitorStatusUp, MonitorStatusDown}, MonitorStatusDegraded},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, MonitorCountryStatusOf(tc.in), "%v", tc.in)
	}

	assert.Equal(t, MonitorStatusUp, MonitorAggregateStatus([]string{MonitorStatusUp, MonitorStatusUnknown}))
	assert.Equal(t, MonitorStatusDown, MonitorAggregateStatus([]string{MonitorStatusDown}))
	assert.Equal(t, MonitorStatusDegraded, MonitorAggregateStatus([]string{MonitorStatusUp, MonitorStatusDown}))
	assert.Equal(t, MonitorStatusDegraded, MonitorAggregateStatus([]string{MonitorStatusDegraded}))
	assert.Equal(t, MonitorStatusUnknown, MonitorAggregateStatus(nil))
}

func TestCountryTransition(t *testing.T) {
	t.Parallel()

	cases := []struct{ prev, next, close, open string }{
		{MonitorStatusUnknown, MonitorStatusUp, "", ""},
		{MonitorStatusUp, MonitorStatusUp, "", ""},
		{MonitorStatusUp, MonitorStatusDegraded, "", MonitorOutageDegraded},
		{MonitorStatusUp, MonitorStatusDown, "", MonitorOutageDown},
		{MonitorStatusDown, MonitorStatusDown, "", ""},
		{MonitorStatusDegraded, MonitorStatusDown, MonitorEndChanged, MonitorOutageDown},
		{MonitorStatusDown, MonitorStatusDegraded, MonitorEndChanged, MonitorOutageDegraded},
		{MonitorStatusDown, MonitorStatusUp, MonitorEndRecovered, ""},
		{MonitorStatusDown, MonitorStatusUnknown, "", ""},
	}
	for _, tc := range cases {
		closeReason, openKind := countryTransition(tc.prev, tc.next)
		assert.Equal(t, tc.close, closeReason, "%s -> %s", tc.prev, tc.next)
		assert.Equal(t, tc.open, openKind, "%s -> %s", tc.prev, tc.next)
	}
}

func TestMonitorHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "timeout", dominantError([]string{"refused", "timeout", "timeout", ""}))
	assert.Equal(t, "dns", dominantError([]string{"refused", "dns"}), "ties resolve to the smallest code")
	assert.Empty(t, dominantError(nil))

	b := monitorBucketOf(time.Date(2026, 9, 30, 10, 7, 59, 0, time.UTC))
	require.Equal(t, time.Date(2026, 9, 30, 10, 5, 0, 0, time.UTC).Unix(), b)
}
