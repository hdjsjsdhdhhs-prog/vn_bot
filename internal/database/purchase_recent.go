package database

import (
 "context"
 "time"

 "gorm.io/gorm"
)

// ReadRecentPurchases returns at most 20 latest Mini App purchases, not a full
// history. Both original buyer and current ownership must match. Expiry uses
// exactly the same transaction boundary as ReadPurchaseOrder.
func (s *Service) ReadRecentPurchases(ctx context.Context, telegramID int64) ([]PurchaseRecord, error) {
 if telegramID <= 0 { return nil, ErrSubscriptionNotFound }
 records := make([]PurchaseRecord, 0)
 err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
  if err := expireUnsubmittedPurchases(tx, telegramID, time.Now().UTC()); err != nil { return err }
  var orders []Order
  if err := tx.Table("orders").Select("orders.*").Joins("JOIN subscriptions ON subscriptions.id = orders.subscription_id").
   Where("orders.purchase_id IS NOT NULL AND orders.buyer_telegram_id = ? AND subscriptions.telegram_id = ?", telegramID, telegramID).
   Order("orders.id DESC").Limit(20).Find(&orders).Error; err != nil { return err }
  for _, order := range orders {
   var product Product
   if err := tx.First(&product, order.ProductID).Error; err != nil { return err }
   records = append(records, PurchaseRecord{Order: order, Product: product})
  }
  return nil
 })
 return records, err
}
