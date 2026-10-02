package service

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trialNodes is the issuance node choice of CreateTrial / BindTrial: only
// active nodes, by id, so a disabled node (no runtime client) is never picked.
func TestTrialNodes_ActiveOnlyOrderedByID(t *testing.T) {
	t.Parallel()

	db := &testutil.DatabaseService{
		GetNodesByPlanNameFunc: func(_ context.Context, planName string) ([]database.Node, error) {
			assert.Equal(t, database.TrialPlanName, planName)
			return []database.Node{{ID: 3, IsActive: true}, {ID: 1, IsActive: false}, {ID: 2, IsActive: true}}, nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{})

	nodes, err := svc.trialNodes(context.Background())
	require.NoError(t, err)
	ids := []uint{}
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	assert.Equal(t, []uint{2, 3}, ids)
}

func TestTrialNodes_OnlyDisabledNodesIsAnError(t *testing.T) {
	t.Parallel()

	db := &testutil.DatabaseService{
		GetNodesByPlanNameFunc: func(context.Context, string) ([]database.Node, error) {
			return []database.Node{{ID: 1, IsActive: false}}, nil
		},
	}
	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{})

	_, err := svc.trialNodes(context.Background())
	require.ErrorContains(t, err, "trial plan has no linked nodes")
}
