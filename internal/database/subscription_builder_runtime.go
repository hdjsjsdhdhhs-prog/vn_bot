package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// BuilderSelection records which level of the priority chain supplied the
// builder used to serve a subscription.
type BuilderSelection string

const (
	// BuilderSelectionSubscription means subscriptions.subscription_builder_id won.
	BuilderSelectionSubscription BuilderSelection = "subscription"
	// BuilderSelectionPlan means the plan default builder was used.
	BuilderSelectionPlan BuilderSelection = "plan"
)

// ResolvedBuilder is the runtime view of the builder that serves a
// subscription: the builder with its Sources (Source preloaded) and Items
// ordered by position, plus the priority level that selected it.
type ResolvedBuilder struct {
	Builder   SubscriptionBuilder
	Selection BuilderSelection
}

// ResolveSubscriptionBuilder selects the builder that serves sub on /sub:
//
//  1. sub.SubscriptionBuilderID (per-subscription override);
//  2. otherwise the plan default (plans.subscription_builder_id);
//  3. otherwise (nil, nil) — the caller keeps the existing pipeline.
//
// A disabled or dangling builder at a level is skipped and the next level is
// consulted, so disabling a builder never breaks the fallback chain.
func (s *Service) ResolveSubscriptionBuilder(ctx context.Context, sub *Subscription) (*ResolvedBuilder, error) {
	if sub == nil {
		return nil, nil
	}

	if sub.SubscriptionBuilderID != nil {
		b, err := s.loadEnabledRuntimeBuilder(ctx, *sub.SubscriptionBuilderID)
		if err != nil {
			return nil, err
		}
		if b != nil {
			return &ResolvedBuilder{Builder: *b, Selection: BuilderSelectionSubscription}, nil
		}
	}

	if sub.PlanID == 0 {
		return nil, nil
	}

	var plan Plan
	err := s.db.WithContext(ctx).Select("id", "subscription_builder_id").First(&plan, sub.PlanID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load plan builder: %w", err)
	}
	if plan.SubscriptionBuilderID == nil {
		return nil, nil
	}

	b, err := s.loadEnabledRuntimeBuilder(ctx, *plan.SubscriptionBuilderID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}

	return &ResolvedBuilder{Builder: *b, Selection: BuilderSelectionPlan}, nil
}

// loadEnabledRuntimeBuilder loads a builder with ordered Sources (Source
// preloaded) and Items. It returns (nil, nil) for a missing or disabled builder.
func (s *Service) loadEnabledRuntimeBuilder(ctx context.Context, id uint) (*SubscriptionBuilder, error) {
	var b SubscriptionBuilder
	err := s.db.WithContext(ctx).
		Preload("Sources", func(db *gorm.DB) *gorm.DB {
			return db.Order("position ASC, source_id ASC")
		}).
		Preload("Sources.Source").
		Preload("Items", func(db *gorm.DB) *gorm.DB {
			return db.Order("position ASC, id ASC")
		}).
		First(&b, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load runtime subscription builder: %w", err)
	}
	if !b.Enabled {
		return nil, nil
	}

	return &b, nil
}
