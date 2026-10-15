-- Migration: 051_remove_legacy_nodes
-- Description: Удаляет старую подсистему VPN-узлов (панели 3x-ui/proxman и
-- legacy fetch-узлы). Подписки собираются только существующими Builders из
-- ProviderSource; локальная регистрация клиентов на панелях, панельная
-- синхронизация и панельный учёт трафика больше не существуют.
--
-- Удаляются только сущности, принадлежавшие исключительно старой подсистеме:
--   * subscription_nodes — очередь синхронизации подписка↔узел;
--   * plan_nodes         — привязка тарифов к узлам;
--   * nodes              — сами узлы (host, api_token, subscription_url);
--   * subscriptions.traffic_reminders_sent — биты уведомлений панельного
--     учёта трафика;
--   * plans.devices_limit / plans.traffic_limit — лимиты, которые
--     применялись только на панелях и больше ничем не контролируются.
--
-- ProviderSource (provider_sources, provider_source_entries), Builders
-- (subscription_builders*), тарифы (plans, products), заказы и платежи не
-- затрагиваются. Порядок DROP — от дочерних таблиц к родительской.

DROP INDEX IF EXISTS idx_subscription_nodes_retry;
DROP INDEX IF EXISTS idx_subscription_nodes_subscription_id;
DROP INDEX IF EXISTS idx_subscription_nodes_node_id;
DROP INDEX IF EXISTS idx_subscription_nodes_status;
DROP INDEX IF EXISTS idx_subscription_nodes_updated_at;
DROP TABLE IF EXISTS subscription_nodes;
DROP TABLE IF EXISTS plan_nodes;
DROP TABLE IF EXISTS nodes;

ALTER TABLE subscriptions DROP COLUMN traffic_reminders_sent;

ALTER TABLE plans DROP COLUMN devices_limit;
ALTER TABLE plans DROP COLUMN traffic_limit;
