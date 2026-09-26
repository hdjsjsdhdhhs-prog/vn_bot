package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// Audited configuration actions (Subscription Builder). They share
// admin_audit_log and its UNIQUE(actor, request_key) idempotency with the
// subscription lifecycle mutations; one request key = one audit row.
const (
	AdminActionSourceCreated              AdminAction = "source_created"
	AdminActionSourceUpdated              AdminAction = "source_updated"
	AdminActionBuilderCreated             AdminAction = "builder_created"
	AdminActionBuilderUpdated             AdminAction = "builder_updated"
	AdminActionPlanBuilderChanged         AdminAction = "plan_builder_changed"
	AdminActionSubscriptionBuilderChanged AdminAction = "subscription_builder_changed"
)

// Configuration rejections. They are returned to the caller and are NOT
// audited: nothing changed, and a retry with the same key re-evaluates.
var (
	ErrBuilderNotFound        = errors.New("subscription builder not found")
	ErrBuilderVersionConflict = errors.New("subscription builder was changed concurrently")
	ErrBuilderNameTaken       = errors.New("subscription builder name is already used")
	ErrProviderSourceInvalid  = errors.New("provider source configuration is invalid")
)

// AdminConfigMeta identifies one audited configuration request.
type AdminConfigMeta struct {
	Actor      string
	RequestKey string
}

// Validate checks the actor and request key with the same rules as
// AdminMutation.Validate.
func (m AdminConfigMeta) Validate() error {
	if m.Actor == "" || len(m.Actor) > AdminMaxActorLength {
		return fmt.Errorf("%w: actor", ErrAdminInvalidRequest)
	}
	if m.RequestKey == "" || len(m.RequestKey) > AdminMaxRequestKey || !isPrintableASCII(m.RequestKey) {
		return fmt.Errorf("%w: request_key", ErrAdminInvalidRequest)
	}
	return nil
}

// AdminConfigResult is the committed (or replayed) outcome of a configuration
// mutation. TargetID identifies the created/changed row, also on replay.
type AdminConfigResult struct {
	Audit    AdminAuditLog
	TargetID uint
	Replayed bool
}

// configChange is what an apply function reports back to the audit helper.
// OldValue/NewValue must be credential-free projections.
type configChange struct {
	targetID       uint
	subscriptionID uint
	oldValue       any
	newValue       any
}

func configHash(action AdminAction, targetType string, targetID uint, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode admin request payload: %w", err)
	}
	canonical := string(action) + "|" + targetType + "|" + strconv.FormatUint(uint64(targetID), 10) + "|" + string(encoded)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// runAdminConfigMutation applies one configuration change and its audit row in
// a single transaction. Replaying the same request key with the same payload
// returns the recorded audit row without re-applying; the same key with a
// different payload is ErrAdminRequestKeyConflict. Any error returned by apply
// rolls everything back and nothing is audited.
func (s *Service) runAdminConfigMutation(ctx context.Context, meta AdminConfigMeta, action AdminAction, targetType string, targetID uint, payload any, apply func(tx *gorm.DB, now time.Time) (configChange, error)) (*AdminConfigResult, error) {
	if err := meta.Validate(); err != nil {
		return nil, err
	}
	hash, err := configHash(action, targetType, targetID, payload)
	if err != nil {
		return nil, err
	}
	var result AdminConfigResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var prior AdminAuditLog
		switch err := tx.Where("actor = ? AND request_key = ?", meta.Actor, meta.RequestKey).First(&prior).Error; {
		case err == nil:
			if prior.RequestHash != hash {
				return ErrAdminRequestKeyConflict
			}
			result = AdminConfigResult{Audit: prior, TargetID: prior.TargetID, Replayed: true}
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("lookup admin request key: %w", err)
		}

		now := time.Now().UTC()
		change, err := apply(tx, now)
		if err != nil {
			return err
		}
		oldJSON, err := json.Marshal(change.oldValue)
		if err != nil {
			return fmt.Errorf("encode audit old value: %w", err)
		}
		newJSON, err := json.Marshal(change.newValue)
		if err != nil {
			return fmt.Errorf("encode audit new value: %w", err)
		}
		entry := AdminAuditLog{
			Actor: meta.Actor, Action: string(action), SubscriptionID: change.subscriptionID, CreatedAt: now,
			OldValue: string(oldJSON), NewValue: string(newJSON), Success: true,
			RequestKey: meta.RequestKey, RequestHash: hash,
			TargetType: targetType, TargetID: change.targetID,
		}
		if err := tx.Create(&entry).Error; err != nil {
			return fmt.Errorf("record admin audit: %w", err)
		}
		result = AdminConfigResult{Audit: entry, TargetID: change.targetID}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
