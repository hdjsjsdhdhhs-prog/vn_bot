package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// CreateProviderSource persists an external VPN provider source.
func (s *Service) CreateProviderSource(ctx context.Context, source *ProviderSource) error {
	err := s.db.WithContext(ctx).Create(source).Error
	if err != nil {
		return fmt.Errorf("create provider source: %w", err)
	}

	return nil
}

// GetProviderSourceByID returns a provider source by its primary key.
func (s *Service) GetProviderSourceByID(ctx context.Context, id uint) (*ProviderSource, error) {
	var source ProviderSource

	err := s.db.WithContext(ctx).First(&source, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProviderSourceNotFound
		}

		return nil, fmt.Errorf("get provider source: %w", err)
	}

	return &source, nil
}
