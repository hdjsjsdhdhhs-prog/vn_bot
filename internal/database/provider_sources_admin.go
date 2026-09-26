package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ListProviderSources returns all provider sources ordered by name.
// Credentials (SubscriptionURL, HWID, UserAgent, Headers) are included because
// this is a DB-layer method; the service/API layer must strip them before
// returning to the browser.
func (s *Service) ListProviderSources(ctx context.Context) ([]ProviderSource, error) {
	var sources []ProviderSource
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&sources).Error; err != nil {
		return nil, fmt.Errorf("list provider sources: %w", err)
	}
	return sources, nil
}

// ProviderSourceUpdateInput holds the mutable admin fields for a ProviderSource.
// Credentials are accepted here; the caller (service layer) must validate them.
type ProviderSourceUpdateInput struct {
	Name        string
	Description string
	Type        string
	// Credential fields — never returned to the browser.
	SubscriptionURL string
	HWID            string
	UserAgent       string
	Headers         string // JSON object
}

// UpdateProviderSource applies mutable fields to an existing source.
func (s *Service) UpdateProviderSource(ctx context.Context, id uint, in ProviderSourceUpdateInput) (*ProviderSource, error) {
	var src ProviderSource
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&src, id).Error; err != nil {
			if isNotFound(err) {
				return ErrProviderSourceNotFound
			}
			return fmt.Errorf("load provider source: %w", err)
		}
		src.Name = in.Name
		src.Description = in.Description
		src.Type = in.Type
		src.SubscriptionURL = in.SubscriptionURL
		src.HWID = in.HWID
		src.UserAgent = in.UserAgent
		src.Headers = in.Headers
		if err := tx.Save(&src).Error; err != nil {
			return fmt.Errorf("update provider source: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// SetProviderSourceEnabled enables or disables a provider source.
func (s *Service) SetProviderSourceEnabled(ctx context.Context, id uint, enabled bool) (*ProviderSource, error) {
	var src ProviderSource
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&src, id).Error; err != nil {
			if isNotFound(err) {
				return ErrProviderSourceNotFound
			}
			return fmt.Errorf("load provider source: %w", err)
		}
		src.Enabled = enabled
		if err := tx.Model(&src).Update("enabled", enabled).Error; err != nil {
			return fmt.Errorf("set provider source enabled: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &src, nil
}

// isNotFound is a local helper to avoid importing gorm in callers.
func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
