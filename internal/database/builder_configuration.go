package database

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// Builder configuration save: the metadata, the linked sources and every rule
// (country rules, node rules, their order and on/off state) of one builder
// are written in ONE transaction guarded by the builder version:
//
//   - in.Version must equal the stored version, otherwise
//     ErrBuilderVersionConflict and nothing is written (a stale editor tab can
//     never overwrite a newer configuration);
//   - any failure rolls back every write, so a builder is never left half
//     saved (sources of the new configuration with rules of the old one);
//   - the version is incremented on every save, also when only sources or
//     rules changed (the /sub cache key covers it);
//   - a rule id must be a rule of THIS builder (ErrBuilderItemNotFound);
//     id 0 creates a rule. Rules of the builder missing from the request are
//     deleted. Rules of other builders are never touched.
//
// It goes through runAdminConfigMutation: audit row (old/new configuration,
// no credentials) and request-key idempotency like every builder mutation.

// AdminActionBuilderConfigured audits a full builder configuration save.
const AdminActionBuilderConfigured AdminAction = "builder_configured"

// Bounds of one builder configuration (same scale as the trial editor).
const (
	BuilderMaxSources     = 50
	BuilderMaxItems       = 500
	builderMaxNameLength  = 128
	builderMaxCustomName  = 128
	builderMaxTextLength  = 512
	builderMaxDescription = 4096
)

// BuilderFieldError names the rejected part of a configuration and a stable
// reason. It unwraps to ErrAdminInvalidRequest (HTTP 400 invalid_request).
type BuilderFieldError struct {
	Field  string
	Reason string
}

func (e *BuilderFieldError) Error() string {
	return ErrAdminInvalidRequest.Error() + ": " + e.Field + ": " + e.Reason
}

func (e *BuilderFieldError) Unwrap() error { return ErrAdminInvalidRequest }

// BuilderConfigItem is one rule of a configuration save; its position is the
// index in BuilderConfigurationInput.Items.
type BuilderConfigItem struct {
	// ID is an existing rule of this builder, 0 for a new rule.
	ID           uint    `json:"id"`
	Kind         string  `json:"kind"`
	SourceID     uint    `json:"source_id"`
	CountryCode  string  `json:"country_code"`
	Fingerprint  string  `json:"fingerprint"`
	OriginalName string  `json:"original_name"`
	CustomName   *string `json:"custom_name"`
	Description  string  `json:"description"`
	Enabled      bool    `json:"enabled"`
}

// BuilderConfigurationInput is the full editable configuration of a builder.
type BuilderConfigurationInput struct {
	ID uint `json:"id"`
	// Version is the version the editor loaded.
	Version      int                 `json:"version"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Enabled      bool                `json:"enabled"`
	ProfileTitle string              `json:"profile_title"`
	SupportURL   string              `json:"support_url"`
	Announce     string              `json:"announce"`
	SourceIDs    []uint              `json:"source_ids"`
	Items        []BuilderConfigItem `json:"items"`
}

// builderConfigAudit is the audited projection of a configuration.
type builderConfigAudit struct {
	builderAuditView
	SourceIDs []uint              `json:"source_ids"`
	Items     []BuilderConfigItem `json:"items"`
}

func builderConfigAuditOf(b *SubscriptionBuilder) builderConfigAudit {
	out := builderConfigAudit{builderAuditView: builderView(b), SourceIDs: []uint{}, Items: []BuilderConfigItem{}}
	for _, s := range b.Sources {
		out.SourceIDs = append(out.SourceIDs, s.SourceID)
	}
	for _, it := range b.Items {
		out.Items = append(out.Items, BuilderConfigItem{
			ID: it.ID, Kind: it.Kind, SourceID: it.SourceID, CountryCode: it.CountryCode, Fingerprint: it.Fingerprint,
			OriginalName: it.OriginalName, CustomName: it.CustomName, Description: it.Description, Enabled: it.Enabled,
		})
	}
	return out
}

// normalizeBuilderConfiguration trims the texts and checks everything that
// does not need the database.
func normalizeBuilderConfiguration(in *BuilderConfigurationInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > builderMaxNameLength {
		return &BuilderFieldError{Field: "name", Reason: "invalid"}
	}
	in.Description = strings.TrimSpace(in.Description)
	in.ProfileTitle = strings.TrimSpace(in.ProfileTitle)
	in.SupportURL = strings.TrimSpace(in.SupportURL)
	in.Announce = strings.TrimSpace(in.Announce)
	if utf8.RuneCountInString(in.Description) > builderMaxDescription {
		return &BuilderFieldError{Field: "description", Reason: "too_long"}
	}
	if utf8.RuneCountInString(in.ProfileTitle) > builderMaxNameLength {
		return &BuilderFieldError{Field: "profile_title", Reason: "too_long"}
	}
	if utf8.RuneCountInString(in.SupportURL) > builderMaxTextLength {
		return &BuilderFieldError{Field: "support_url", Reason: "too_long"}
	}
	if utf8.RuneCountInString(in.Announce) > builderMaxTextLength {
		return &BuilderFieldError{Field: "announce", Reason: "too_long"}
	}

	if len(in.SourceIDs) > BuilderMaxSources {
		return &BuilderFieldError{Field: "source_ids", Reason: "too_many"}
	}
	seenSources := make(map[uint]bool, len(in.SourceIDs))
	for _, id := range in.SourceIDs {
		if id == 0 || seenSources[id] {
			return &BuilderFieldError{Field: "source_ids", Reason: "duplicate"}
		}
		seenSources[id] = true
	}

	if len(in.Items) > BuilderMaxItems {
		return &BuilderFieldError{Field: "items", Reason: "too_many"}
	}
	seenItems := make(map[uint]bool, len(in.Items))
	for i := range in.Items {
		it := &in.Items[i]
		if it.ID != 0 {
			if seenItems[it.ID] {
				return &BuilderFieldError{Field: "items", Reason: "duplicate"}
			}
			seenItems[it.ID] = true
		}
		if it.SourceID == 0 {
			return &BuilderFieldError{Field: "items", Reason: "source_required"}
		}
		if it.CustomName != nil {
			name := strings.TrimSpace(*it.CustomName)
			if name == "" {
				it.CustomName = nil
			} else if utf8.RuneCountInString(name) > builderMaxCustomName {
				return &BuilderFieldError{Field: "items", Reason: "custom_name_too_long"}
			} else {
				it.CustomName = &name
			}
		}
		it.Description = strings.TrimSpace(it.Description)
		switch it.Kind {
		case BuilderItemKindCountry:
			// '' = entries without a country (database.CountryMatches).
			it.CountryCode = strings.ToUpper(strings.TrimSpace(it.CountryCode))
			if it.CountryCode != "" && !trialCountryPattern.MatchString(it.CountryCode) {
				return &BuilderFieldError{Field: "items", Reason: "invalid_country"}
			}
			it.Fingerprint, it.OriginalName = "", ""
		case BuilderItemKindNode:
			it.Fingerprint = strings.TrimSpace(it.Fingerprint)
			if it.Fingerprint == "" && strings.TrimSpace(it.OriginalName) == "" {
				return &BuilderFieldError{Field: "items", Reason: "node_required"}
			}
			if len(it.Fingerprint) > 64 || utf8.RuneCountInString(it.OriginalName) > 255 {
				return &BuilderFieldError{Field: "items", Reason: "too_long"}
			}
			it.CountryCode = ""
		default:
			return &BuilderFieldError{Field: "items", Reason: "invalid_kind"}
		}
	}
	return nil
}

// checkBuilderConfigSources verifies every referenced provider source exists.
func checkBuilderConfigSources(tx *gorm.DB, in *BuilderConfigurationInput) error {
	want := make(map[uint]bool)
	for _, id := range in.SourceIDs {
		want[id] = true
	}
	for _, it := range in.Items {
		want[it.SourceID] = true
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	var found int64
	if err := tx.Model(&ProviderSource{}).Where("id IN ?", ids).Count(&found).Error; err != nil {
		return fmt.Errorf("check builder sources: %w", err)
	}
	if int(found) != len(ids) {
		return &BuilderFieldError{Field: "source_ids", Reason: "source_not_found"}
	}
	return nil
}

// SaveBuilderConfiguration atomically replaces the metadata, sources and rules
// of a builder (see the file comment). TargetID of the result is the builder.
func (s *Service) SaveBuilderConfiguration(ctx context.Context, meta AdminConfigMeta, in BuilderConfigurationInput) (*AdminConfigResult, error) {
	if in.ID == 0 {
		return nil, ErrBuilderNotFound
	}
	if err := normalizeBuilderConfiguration(&in); err != nil {
		return nil, err
	}
	return s.runAdminConfigMutation(ctx, meta, AdminActionBuilderConfigured, AdminTargetBuilder, in.ID, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			svc := &Service{db: tx}
			current, err := svc.GetBuilder(ctx, in.ID)
			if err != nil {
				return configChange{}, err
			}
			if current.Version != in.Version {
				return configChange{}, ErrBuilderVersionConflict
			}
			owned := make(map[uint]bool, len(current.Items))
			for _, it := range current.Items {
				owned[it.ID] = true
			}
			keep := make([]uint, 0, len(in.Items))
			for _, it := range in.Items {
				if it.ID == 0 {
					continue
				}
				if !owned[it.ID] {
					return configChange{}, ErrBuilderItemNotFound
				}
				keep = append(keep, it.ID)
			}
			if err := checkBuilderConfigSources(tx, &in); err != nil {
				return configChange{}, err
			}
			old := builderConfigAuditOf(current)

			// Metadata + version first: the WHERE version guard also holds
			// when another transaction committed after GetBuilder above.
			res := tx.Model(&SubscriptionBuilder{}).Where("id = ? AND version = ?", in.ID, in.Version).Updates(map[string]any{
				"name": in.Name, "description": in.Description, "enabled": in.Enabled,
				"profile_title": in.ProfileTitle, "support_url": in.SupportURL, "announce": in.Announce,
				"version": in.Version + 1, "updated_at": now,
			})
			if res.Error != nil {
				if isUniqueConstraintError(res.Error, "subscription_builders.name") {
					return configChange{}, ErrBuilderNameTaken
				}
				return configChange{}, fmt.Errorf("update subscription builder: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return configChange{}, ErrBuilderVersionConflict
			}

			if err := tx.Where("builder_id = ?", in.ID).Delete(&SubscriptionBuilderSource{}).Error; err != nil {
				return configChange{}, fmt.Errorf("clear builder sources: %w", err)
			}
			for i, id := range in.SourceIDs {
				if err := tx.Create(&SubscriptionBuilderSource{BuilderID: in.ID, SourceID: id, Position: i}).Error; err != nil {
					return configChange{}, fmt.Errorf("add builder source %d: %w", id, err)
				}
			}

			drop := tx.Where("builder_id = ?", in.ID)
			if len(keep) > 0 {
				drop = drop.Where("id NOT IN ?", keep)
			}
			if err := drop.Delete(&SubscriptionBuilderItem{}).Error; err != nil {
				return configChange{}, fmt.Errorf("delete removed builder items: %w", err)
			}
			for i, it := range in.Items {
				item := SubscriptionBuilderItem{
					ID: it.ID, BuilderID: in.ID, Kind: it.Kind, SourceID: it.SourceID, CountryCode: it.CountryCode,
					Fingerprint: it.Fingerprint, OriginalName: it.OriginalName, CustomName: it.CustomName,
					Description: it.Description, Position: i, Enabled: it.Enabled,
				}
				if it.ID == 0 {
					if err := createBuilderItem(tx, &item); err != nil {
						return configChange{}, err
					}
					continue
				}
				res := tx.Model(&SubscriptionBuilderItem{}).Where("id = ? AND builder_id = ?", it.ID, in.ID).Updates(builderItemColumns(item))
				if res.Error != nil {
					return configChange{}, fmt.Errorf("update builder item: %w", res.Error)
				}
				if res.RowsAffected == 0 {
					return configChange{}, ErrBuilderItemNotFound
				}
			}

			saved, err := svc.GetBuilder(ctx, in.ID)
			if err != nil {
				return configChange{}, err
			}
			return configChange{targetID: in.ID, oldValue: old, newValue: builderConfigAuditOf(saved)}, nil
		})
}
