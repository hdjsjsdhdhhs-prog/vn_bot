package subserver

import "errors"

var (
	ErrSubscriptionNotFound      = errors.New("subscription not found")
	ErrNoSubscriptionItems       = errors.New("no subscription items found")
	ErrProviderSourceUnavailable = errors.New("provider source unavailable")
	// ErrBuilderUnavailable is a configuration error: the subscription has no
	// enabled SubscriptionBuilder (neither its own override nor its plan
	// default) and no linked ProviderSource. /sub answers 503; there is no
	// fallback to any other serving route.
	ErrBuilderUnavailable = errors.New("subscription builder is not configured")
)
