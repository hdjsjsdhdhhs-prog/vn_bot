package service

import (
 "context"

 "github.com/kereal/rs8kvn_bot/internal/database"
)

type recentPurchaseRepository interface {
 ReadRecentPurchases(context.Context, int64) ([]database.PurchaseRecord, error)
}

// Recent restores purchases after a WebView restart without trusting a client
// identity or exposing provider/checkout credentials. At most 20 latest orders.
func (p *PurchaseService) Recent(ctx context.Context) ([]PurchaseOrderInfo, error) {
 id, err := p.customer(ctx)
 if err != nil { return nil, err }
 repo, ok := p.repo.(recentPurchaseRepository)
 if !ok { return nil, ErrPurchaseUnavailable }
 records, err := repo.ReadRecentPurchases(ctx, id)
 if err != nil { return nil, safePurchaseError(err) }
 result := make([]PurchaseOrderInfo, 0, len(records))
 for i := range records {
  info, err := purchaseInfo(&records[i], id)
  if err != nil { return nil, err }
  result = append(result, *info)
 }
 return result, nil
}
