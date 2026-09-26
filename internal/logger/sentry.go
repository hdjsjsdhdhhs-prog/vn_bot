package logger

import "github.com/getsentry/sentry-go"

// SanitizeSentryEvent is the BeforeSend hook for error events. Sentry has already
// built exceptions from the original panic/error at this point. Only diagnostic
// text is changed: types, stack frames, mechanisms, context and EventHint remain
// intact. Do not attach raw request/response bodies or credentials to scopes.
func SanitizeSentryEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}
	event.Message = Sanitize(event.Message)
	for i := range event.Exception {
		event.Exception[i].Value = Sanitize(event.Exception[i].Value)
	}
	// Scope tags may be shared with other events; never redact the source map.
	if event.Tags != nil {
		tags := make(map[string]string, len(event.Tags))
		for key, value := range event.Tags {
			tags[key] = Sanitize(value)
		}
		event.Tags = tags
	}
	return event
}
