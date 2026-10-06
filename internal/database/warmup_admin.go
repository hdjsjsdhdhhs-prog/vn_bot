package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Browser-admin operations of the customer warm-up that are not scenario or
// settings saves: audited image upload/delete and the overview summary.

// ErrWarmupImageInUse: a scenario still shows the picture.
var ErrWarmupImageInUse = errors.New("warmup image is used by a scenario")

// warmupImageAudit is the audited projection of an image (never the bytes).
type warmupImageAudit struct {
	ID        uint   `json:"id"`
	Mime      string `json:"mime"`
	SizeBytes int    `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// warmupImageRequest is what an upload's request key is bound to: the digest
// stands for the bytes, so a replay is recognized without hashing 2 MB of
// JSON.
type warmupImageRequest struct {
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
	Mime      string `json:"mime"`
}

// UploadWarmupImage stores a validated picture (audited, idempotent by
// request key). The same bytes uploaded again resolve to the existing row.
// TargetID of the result is the image ID.
func (s *Service) UploadWarmupImage(ctx context.Context, meta AdminConfigMeta, mime string, data []byte) (*AdminConfigResult, error) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	request := warmupImageRequest{SHA256: digest, SizeBytes: len(data), Mime: mime}
	return s.runAdminConfigMutation(ctx, meta, AdminActionWarmupImageUploaded, AdminTargetWarmupImage, 0, request,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			img := WarmupImage{Mime: mime, SizeBytes: len(data), SHA256: digest, Data: data, CreatedAt: now}
			err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "sha256"}}, DoNothing: true}).Create(&img).Error
			if err != nil {
				return configChange{}, fmt.Errorf("create warmup image: %w", err)
			}
			var stored WarmupImage
			err = tx.Omit("data").Where("sha256 = ?", digest).First(&stored).Error
			if err != nil {
				return configChange{}, fmt.Errorf("load warmup image: %w", err)
			}
			return configChange{targetID: stored.ID, newValue: warmupImageAudit{
				ID: stored.ID, Mime: stored.Mime, SizeBytes: stored.SizeBytes, SHA256: stored.SHA256,
			}}, nil
		})
}

// WarmupImageUsage counts the scenarios that show the picture.
func (s *Service) WarmupImageUsage(ctx context.Context, id uint) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&WarmupScenario{}).Where("image_id = ?", id).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count warmup image usage: %w", err)
	}
	return n, nil
}

// DeleteWarmupImage removes a picture no scenario uses any more (audited,
// idempotent by request key). Sent messages keep their Telegram copy.
func (s *Service) DeleteWarmupImage(ctx context.Context, meta AdminConfigMeta, id uint) (*AdminConfigResult, error) {
	return s.runAdminConfigMutation(ctx, meta, AdminActionWarmupImageDeleted, AdminTargetWarmupImage, id, struct {
		ID uint `json:"id"`
	}{id},
		func(tx *gorm.DB, _ time.Time) (configChange, error) {
			var img WarmupImage
			err := tx.Omit("data").First(&img, id).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return configChange{}, ErrWarmupImageNotFound
			}
			if err != nil {
				return configChange{}, fmt.Errorf("load warmup image: %w", err)
			}
			var used int64
			err = tx.Model(&WarmupScenario{}).Where("image_id = ?", id).Count(&used).Error
			if err != nil {
				return configChange{}, fmt.Errorf("count warmup image usage: %w", err)
			}
			if used > 0 {
				return configChange{}, ErrWarmupImageInUse
			}
			err = tx.Delete(&WarmupImage{}, id).Error
			if err != nil {
				return configChange{}, fmt.Errorf("delete warmup image: %w", err)
			}
			return configChange{targetID: id, oldValue: warmupImageAudit{
				ID: img.ID, Mime: img.Mime, SizeBytes: img.SizeBytes, SHA256: img.SHA256,
			}}, nil
		})
}

// WarmupSummary is the overview of the admin page. SentToday counts messages
// delivered since the start of the current MSK day (the bot shows users MSK
// dates), Sent7d those of the last 7 days; ScenarioStats has one entry per
// scenario that ever delivered a message.
type WarmupSummary struct {
	Scenarios        int                  `json:"scenarios"`
	EnabledScenarios int                  `json:"enabled_scenarios"`
	Last24h          map[string]int64     `json:"last_24h"`
	SentToday        int64                `json:"sent_today"`
	Sent7d           int64                `json:"sent_7d"`
	ActiveCoupons    int64                `json:"active_coupons"`
	CouponsCreated   int64                `json:"coupons_created"`
	Contacts         int64                `json:"contacts"`
	BlockedContacts  int64                `json:"blocked_contacts"`
	ScenarioStats    []WarmupScenarioStat `json:"scenario_stats"`
}

// WarmupScenarioStat counts the delivered messages of one scenario.
type WarmupScenarioStat struct {
	ScenarioID uint       `json:"scenario_id"`
	Sent       int64      `json:"sent"`
	LastSentAt *time.Time `json:"last_sent_at"`
}

// warmupAggregateTimeLayouts are the forms SQLite returns for MAX() of a
// DATETIME column: the aggregate has no declared type, so the driver hands
// back the stored text (go-sqlite3 writes "2006-01-02 15:04:05.999999999-07:00").
var warmupAggregateTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
}

func parseWarmupAggregateTime(raw string) (time.Time, bool) {
	for _, layout := range warmupAggregateTimeLayouts {
		at, err := time.Parse(layout, raw)
		if err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// warmupSummaryZone is the calendar of "sent today" (MSK, no DST since 2014).
var warmupSummaryZone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}()

// GetWarmupSummary counts scenarios, the deliveries of the last 24 hours by
// status, delivered messages (today, 7 days, per scenario), coupons and the
// contacts (all aggregates in SQL).
func (s *Service) GetWarmupSummary(ctx context.Context, now time.Time) (*WarmupSummary, error) {
	db := s.db.WithContext(ctx)
	now = now.UTC()
	summary := &WarmupSummary{Last24h: map[string]int64{
		WarmupDeliverySending: 0, WarmupDeliverySent: 0, WarmupDeliveryRetry: 0, WarmupDeliveryFailed: 0, WarmupDeliveryBlocked: 0,
	}}

	var scenarios struct {
		Total   int
		Enabled int
	}
	err := db.Model(&WarmupScenario{}).Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN enabled THEN 1 ELSE 0 END), 0) AS enabled").
		Scan(&scenarios).Error
	if err != nil {
		return nil, fmt.Errorf("count warmup scenarios: %w", err)
	}
	summary.Scenarios, summary.EnabledScenarios = scenarios.Total, scenarios.Enabled

	var byStatus []struct {
		Status string
		N      int64
	}
	err = db.Model(&WarmupDelivery{}).Select("status, COUNT(*) AS n").
		Where("created_at >= ?", now.Add(-24*time.Hour)).Group("status").Scan(&byStatus).Error
	if err != nil {
		return nil, fmt.Errorf("count warmup deliveries: %w", err)
	}
	for _, row := range byStatus {
		summary.Last24h[row.Status] = row.N
	}

	local := now.In(warmupSummaryZone)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, warmupSummaryZone).UTC()
	var sent struct {
		Today int64
		Week  int64
	}
	err = db.Model(&WarmupDelivery{}).
		Select("COALESCE(SUM(CASE WHEN sent_at >= ? THEN 1 ELSE 0 END), 0) AS today, COUNT(*) AS week", dayStart).
		Where("status = ? AND sent_at >= ?", WarmupDeliverySent, now.Add(-7*24*time.Hour)).Scan(&sent).Error
	if err != nil {
		return nil, fmt.Errorf("count sent warmup deliveries: %w", err)
	}
	summary.SentToday, summary.Sent7d = sent.Today, sent.Week

	summary.ScenarioStats = []WarmupScenarioStat{}
	var stats []struct {
		ScenarioID uint
		Sent       int64
		LastSentAt *string
	}
	err = db.Model(&WarmupDelivery{}).Select("scenario_id, COUNT(*) AS sent, MAX(sent_at) AS last_sent_at").
		Where("status = ?", WarmupDeliverySent).Group("scenario_id").Order("scenario_id").Scan(&stats).Error
	if err != nil {
		return nil, fmt.Errorf("count warmup deliveries per scenario: %w", err)
	}
	for _, row := range stats {
		stat := WarmupScenarioStat{ScenarioID: row.ScenarioID, Sent: row.Sent}
		if row.LastSentAt != nil {
			if at, ok := parseWarmupAggregateTime(*row.LastSentAt); ok {
				stat.LastSentAt = &at
			}
		}
		summary.ScenarioStats = append(summary.ScenarioStats, stat)
	}

	err = db.Model(&WarmupCoupon{}).Where("status = ? AND expires_at > ?", WarmupCouponIssued, now).Count(&summary.ActiveCoupons).Error
	if err != nil {
		return nil, fmt.Errorf("count warmup coupons: %w", err)
	}
	err = db.Model(&WarmupCoupon{}).Count(&summary.CouponsCreated).Error
	if err != nil {
		return nil, fmt.Errorf("count created warmup coupons: %w", err)
	}
	var contacts struct {
		Total   int64
		Blocked int64
	}
	err = db.Model(&WarmupContact{}).Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN blocked_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS blocked").
		Scan(&contacts).Error
	if err != nil {
		return nil, fmt.Errorf("count warmup contacts: %w", err)
	}
	summary.Contacts, summary.BlockedContacts = contacts.Total, contacts.Blocked
	return summary, nil
}
