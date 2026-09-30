package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// JournalRepository is the read seam of the admin journal. *database.Service
// implements it; the journal itself is written only by lifecycle code.
type JournalRepository interface {
	ListJournal(ctx context.Context, f database.JournalFilter) ([]database.JournalEvent, int64, error)
	GetJournalEvent(ctx context.Context, id uint) (*database.JournalEvent, error)
}

var _ JournalRepository = (*database.Service)(nil)

// ErrJournalUnavailable means the repository has no journal (test doubles).
var ErrJournalUnavailable = errors.New("journal is not available")

// JournalEventView is the browser projection of a journal row: details are
// decoded JSON (before/after states, payment data) instead of a string.
type JournalEventView struct {
	database.JournalEvent
	Details json.RawMessage `json:"details"`
}

// JournalPage is one page of the journal, newest first.
type JournalPage struct {
	Events []JournalEventView `json:"events"`
	Total  int64              `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

func journalViewOf(event database.JournalEvent) JournalEventView {
	details := json.RawMessage(event.Details)
	if !json.Valid(details) {
		details = json.RawMessage("{}")
	}
	return JournalEventView{JournalEvent: event, Details: details}
}

func (s *AdminService) journal() (JournalRepository, error) {
	repo, ok := s.repo.(JournalRepository)
	if !ok {
		return nil, ErrJournalUnavailable
	}
	return repo, nil
}

// ListJournal pages journal events. Unknown filter values are rejected with
// database.ErrAdminInvalidRequest rather than silently matching nothing.
func (s *AdminService) ListJournal(ctx context.Context, f database.JournalFilter) (*JournalPage, error) {
	if f.Type != "" && !database.IsKnownJournalEventType(f.Type) {
		return nil, fmt.Errorf("%w: type", database.ErrAdminInvalidRequest)
	}
	if f.PlanKind != "" && !database.IsKnownPlanKind(f.PlanKind) {
		return nil, fmt.Errorf("%w: plan_kind", database.ErrAdminInvalidRequest)
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return nil, fmt.Errorf("%w: period", database.ErrAdminInvalidRequest)
	}
	if f.Limit <= 0 {
		f.Limit = database.AdminDefaultPageSize
	}
	if f.Limit > database.AdminMaxPageSize {
		f.Limit = database.AdminMaxPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	repo, err := s.journal()
	if err != nil {
		return nil, err
	}
	events, total, err := repo.ListJournal(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("admin journal: %w", err)
	}
	page := &JournalPage{Events: make([]JournalEventView, 0, len(events)), Total: total, Limit: f.Limit, Offset: f.Offset}
	for _, event := range events {
		page.Events = append(page.Events, journalViewOf(event))
	}
	return page, nil
}

// GetJournalEvent returns one journal event (database.ErrJournalEventNotFound
// for an unknown ID).
func (s *AdminService) GetJournalEvent(ctx context.Context, id uint) (*JournalEventView, error) {
	repo, err := s.journal()
	if err != nil {
		return nil, err
	}
	event, err := repo.GetJournalEvent(ctx, id)
	if err != nil {
		return nil, err
	}
	view := journalViewOf(*event)
	return &view, nil
}
