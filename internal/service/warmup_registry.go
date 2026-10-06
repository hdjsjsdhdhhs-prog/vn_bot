package service

import (
	"slices"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// Code registries of the customer warm-up. Scenarios are rows in the database
// (new scenarios need no schema change); what a scenario may reference —
// trigger types, template variables, callback actions, condition keys — is
// fixed here, so a stored scenario can never point at behaviour the code does
// not implement.

// Trigger types: the lifecycle event a scenario is scheduled from.
const (
	// WarmupTriggerBotStarted: the first live /start of a user.
	WarmupTriggerBotStarted = "bot_started"
	// WarmupTriggerTrialActivated: a trial activated (bound) in Telegram.
	WarmupTriggerTrialActivated = "trial_activated"
	// WarmupTriggerTrialPeriodEnd: the end of the trial period of a Telegram
	// trial (binding keeps the user on the free plan; this is the nominal end).
	WarmupTriggerTrialPeriodEnd = "trial_period_end"
	// WarmupTriggerPaidExpiry: the expiry of an active paid subscription.
	WarmupTriggerPaidExpiry = "paid_expiry"
	// WarmupTriggerPaidExpired: a paid term that ended (access downgraded).
	WarmupTriggerPaidExpired = "paid_expired"
)

// WarmupTrigger describes one trigger type for validation and the admin.
type WarmupTrigger struct {
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Timings     []string `json:"timings"`
}

var warmupTriggers = []WarmupTrigger{
	{Type: WarmupTriggerBotStarted, Label: "Первый запуск бота",
		Description: "Первый /start пользователя (исторические пользователи не считаются)",
		Timings:     []string{database.WarmupTimingAfter}},
	{Type: WarmupTriggerTrialActivated, Label: "Пробник активирован",
		Description: "Пробная подписка привязана к Telegram",
		Timings:     []string{database.WarmupTimingAfter}},
	{Type: WarmupTriggerTrialPeriodEnd, Label: "Окончание пробного периода",
		Description: "Срок, который был у пробника при активации",
		Timings:     []string{database.WarmupTimingBefore, database.WarmupTimingAfter}},
	{Type: WarmupTriggerPaidExpiry, Label: "Окончание платной подписки",
		Description: "Срок действия активной платной подписки",
		Timings:     []string{database.WarmupTimingBefore}},
	{Type: WarmupTriggerPaidExpired, Label: "Платная подписка закончилась",
		Description: "Платный срок истёк, подписка переведена на бесплатный тариф",
		Timings:     []string{database.WarmupTimingAfter}},
}

// WarmupTriggers returns the trigger registry.
func WarmupTriggers() []WarmupTrigger {
	return append([]WarmupTrigger(nil), warmupTriggers...)
}

func warmupTriggerByType(t string) (WarmupTrigger, bool) {
	for _, tr := range warmupTriggers {
		if tr.Type == t {
			return tr, true
		}
	}
	return WarmupTrigger{}, false
}

func (t WarmupTrigger) allowsTiming(timing string) bool {
	return slices.Contains(t.Timings, timing)
}

// Template variables.
const (
	WarmupVarUsername        = "username"
	WarmupVarTelegramID      = "telegram_id"
	WarmupVarPlanName        = "plan_name"
	WarmupVarTrialDays       = "trial_days"
	WarmupVarTrialDuration   = "trial_duration"
	WarmupVarExpireDate      = "expire_date"
	WarmupVarDiscountCode    = "discount_code"
	WarmupVarDiscountPercent = "discount_percent"
)

// WarmupVariable describes one {{variable}}. Triggers lists where it has a
// value (nil: every trigger); Discount variables need discount_percent > 0.
// Sample is the preview value; it is deliberately long so the length check of
// a preview is not looser than a real message.
type WarmupVariable struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Sample      string   `json:"sample"`
	Triggers    []string `json:"triggers"`
	Discount    bool     `json:"discount"`
}

var warmupExpiryTriggers = []string{WarmupTriggerTrialPeriodEnd, WarmupTriggerPaidExpiry, WarmupTriggerPaidExpired}

var warmupVariables = []WarmupVariable{
	{Name: WarmupVarUsername, Label: "Имя пользователя", Description: "@username, без него — «друг»", Sample: "@dictator_vpn_user_long_name"},
	{Name: WarmupVarTelegramID, Label: "Telegram ID", Description: "Числовой ID пользователя", Sample: "1234567890"},
	{Name: WarmupVarPlanName, Label: "Тариф", Description: "Название платного тарифа подписки", Sample: "Premium 12 месяцев",
		Triggers: []string{WarmupTriggerPaidExpiry, WarmupTriggerPaidExpired}},
	{Name: WarmupVarTrialDays, Label: "Дней пробника", Description: "Длительность пробного периода в днях (число)", Sample: "3"},
	{Name: WarmupVarTrialDuration, Label: "Длительность пробника", Description: "Длительность пробного периода словами («3 дня», «24 часа»)", Sample: "72 часа"},
	{Name: WarmupVarExpireDate, Label: "Дата окончания", Description: "Дата и время окончания (МСК)", Sample: "31.12.2026 23:59 МСК",
		Triggers: warmupExpiryTriggers},
	{Name: WarmupVarDiscountCode, Label: "Код скидки", Description: "Персональный купон этого сообщения", Sample: "TEST-7K3P9QXD", Discount: true},
	{Name: WarmupVarDiscountPercent, Label: "Размер скидки", Description: "Процент скидки купона", Sample: "15", Discount: true},
}

// WarmupVariables returns the variable registry.
func WarmupVariables() []WarmupVariable {
	return append([]WarmupVariable(nil), warmupVariables...)
}

func warmupVariableByName(name string) (WarmupVariable, bool) {
	for _, v := range warmupVariables {
		if v.Name == name {
			return v, true
		}
	}
	return WarmupVariable{}, false
}

func (v WarmupVariable) availableFor(trigger string) bool {
	if v.Triggers == nil {
		return true
	}
	return slices.Contains(v.Triggers, trigger)
}

// Button kinds.
const (
	WarmupButtonURL      = "url"
	WarmupButtonWebApp   = "web_app"
	WarmupButtonCallback = "callback"
)

// WarmupCallbackAction is an existing user-facing callback of the bot
// (internal/bot/callback.go) that a warm-up button may trigger. Arbitrary
// callback_data is never accepted.
type WarmupCallbackAction struct {
	Data  string `json:"data"`
	Label string `json:"label"`
}

var warmupCallbackActions = []WarmupCallbackAction{
	{Data: "menu_subscription", Label: "Моя подписка"},
	{Data: "buy_premium_list", Label: "Тарифы (купить Premium)"},
	{Data: "create_subscription", Label: "Получить подписку"},
	{Data: "back_to_start", Label: "Главное меню"},
	{Data: "menu_help", Label: "Помощь"},
	{Data: "menu_support", Label: "Поддержка"},
	{Data: "menu_donate", Label: "Поддержать проект"},
	{Data: "share_invite", Label: "Пригласить друга"},
	{Data: "menu_documents", Label: "Документы"},
}

// WarmupCallbackActions returns the callback whitelist.
func WarmupCallbackActions() []WarmupCallbackAction {
	return append([]WarmupCallbackAction(nil), warmupCallbackActions...)
}

func isWarmupCallbackAllowed(data string) bool {
	for _, a := range warmupCallbackActions {
		if a.Data == data {
			return true
		}
	}
	return false
}

// WarmupConditionKey describes one condition for the admin.
type WarmupConditionKey struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // "bool" | "languages"
}

var warmupConditionKeys = []WarmupConditionKey{
	{Key: "had_trial", Label: "Был пробник", Description: "Пользователь активировал пробник в Telegram", Kind: "bool"},
	{Key: "has_subscription", Label: "Есть подписка", Description: "У пользователя есть любая подписка в боте", Kind: "bool"},
	{Key: "has_active_paid", Label: "Активная платная", Description: "Действующая платная подписка", Kind: "bool"},
	{Key: "ever_paid", Label: "Платил", Description: "Была хотя бы одна успешная оплата", Kind: "bool"},
	{Key: "connected", Label: "Подключался", Description: "VPN-клиент хотя бы раз забрал конфиг подписки", Kind: "bool"},
	{Key: "languages", Label: "Язык Telegram", Description: "Коды языка (ru, uk, en…); пусто — любой", Kind: "languages"},
}

// WarmupConditionKeys returns the condition registry.
func WarmupConditionKeys() []WarmupConditionKey {
	return append([]WarmupConditionKey(nil), warmupConditionKeys...)
}
