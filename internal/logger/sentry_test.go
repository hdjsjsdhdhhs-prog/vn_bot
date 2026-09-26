package logger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Capture the real SDK output without sending events over the network.
type redactionTransport struct {
	events []*sentry.Event
}

func (t *redactionTransport) Configure(sentry.ClientOptions) {}
func (t *redactionTransport) SendEvent(event *sentry.Event) { t.events = append(t.events, event) }
func (t *redactionTransport) Flush(time.Duration) bool { return true }
func (t *redactionTransport) FlushWithContext(context.Context) bool { return true }
func (t *redactionTransport) Close() {}

func TestRecover_PreservesSentryPanicSemantics(t *testing.T) {
	isolateSecrets(t)
	RegisterSecrets("panic-fixture")
	cause := errors.New("failure panic-fixture")
	wrapped := fmt.Errorf("outer: %w", cause)
	for _, tc := range []struct {
		name string
		value any
		wantExceptions bool
	}{
		{"error chain", wrapped, true},
		{"string", "failure panic-fixture", false},
		{"object", &struct{ Detail string }{"panic-fixture"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &redactionTransport{}
			var originalExceptions []sentry.Exception
			var receivedHint *sentry.EventHint
			client, err := sentry.NewClient(sentry.ClientOptions{
				Dsn: "https://public@example.invalid/1",
				Transport: transport,
				AttachStacktrace: true,
				BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
					receivedHint = hint
					originalExceptions = append([]sentry.Exception(nil), event.Exception...)
					return SanitizeSentryEvent(event, hint)
				},
			})
			require.NoError(t, err)
			hub := sentry.CurrentHub()
			previousClient := hub.Client()
			hub.BindClient(client)
			hub.PushScope()
			hub.Scope().SetContext("diagnostic", sentry.Context{"component": "worker"})
			hub.Scope().SetTag("detail", "failure panic-fixture")
			core, logs := observer.New(zapcore.ErrorLevel)
			previousLog := Log
			Log = zap.New(core)
			t.Cleanup(func() {
				Log = previousLog
				hub.PopScope()
				hub.BindClient(previousClient)
			})

			func() {
				defer Recover("worker")
				panic(tc.value)
			}()

			require.Len(t, transport.events, 1, "Recover must not create a second message event")
			require.NotNil(t, receivedHint)
			assert.Equal(t, tc.value, receivedHint.RecoveredException)
			if tc.wantExceptions {
				assert.Same(t, wrapped, receivedHint.RecoveredException)
			}
			event := transport.events[0]
			assert.Equal(t, sentry.LevelFatal, event.Level)
			assert.Equal(t, "worker", event.Contexts["diagnostic"]["component"])
			assert.Equal(t, "failure [redacted]", event.Tags["detail"])
			assert.NotContains(t, event.Message, "panic-fixture")
			if tc.wantExceptions {
				require.Len(t, event.Exception, 2)
				assert.ErrorIs(t, wrapped, cause)
				for i, exception := range event.Exception {
					assert.NotContains(t, exception.Value, "panic-fixture")
					assert.Contains(t, exception.Value, "failure")
					assert.Equal(t, originalExceptions[i].Type, exception.Type)
					// Stacktrace pointer must be untouched; the SDK leaves it nil for
					// inner plain errors, so compare pointers rather than assert.Same.
					assert.True(t, originalExceptions[i].Stacktrace == exception.Stacktrace,
						"exception[%d] stacktrace pointer changed", i)
					assert.Equal(t, originalExceptions[i].Mechanism, exception.Mechanism)
				}
				// sentry-go attaches the current stack only to the outermost error,
				// which is the last element after the SDK reverses the chain.
				outer := event.Exception[len(event.Exception)-1]
				require.NotNil(t, outer.Stacktrace)
				assert.NotEmpty(t, outer.Stacktrace.Frames)
			} else {
				assert.Empty(t, event.Exception)
				require.NotEmpty(t, event.Threads)
				require.NotNil(t, event.Threads[0].Stacktrace)
				assert.NotEmpty(t, event.Threads[0].Stacktrace.Frames)
			}
			entries := logs.All()
			require.Len(t, entries, 1)
			assert.NotContains(t, entries[0].ContextMap()["panic"], "panic-fixture")
			assert.Contains(t, entries[0].ContextMap()["stack"], "TestRecover_PreservesSentryPanicSemantics")
		})
	}
}

func TestSanitizeSentryEvent_PreservesSourceTagsAndHint(t *testing.T) {
	isolateSecrets(t)
	RegisterSecrets("event-fixture")
	tags := map[string]string{"detail": "event-fixture"}
	panicValue := errors.New("event-fixture")
	hint := &sentry.EventHint{RecoveredException: panicValue}
	event := &sentry.Event{Message: "failure event-fixture", Tags: tags}
	assert.Same(t, event, SanitizeSentryEvent(event, hint))
	assert.Equal(t, "event-fixture", tags["detail"])
	assert.Equal(t, redacted, event.Tags["detail"])
	assert.Equal(t, "failure [redacted]", event.Message)
	assert.Same(t, panicValue, hint.RecoveredException)
	assert.Nil(t, SanitizeSentryEvent(nil, nil))
}
