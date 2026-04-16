package server

import "testing"

func TestWebhookNotifyNonBlocking(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{}, 1)

	// Fill the channel
	ch <- struct{}{}

	// Second send should not block
	notifyInstallationChange(ch)

	// Should still have exactly one signal
	select {
	case <-ch:
	default:
		t.Error("channel should have had a signal")
	}
}

func TestWebhookNotifyNil(t *testing.T) {
	t.Parallel()
	// Should not panic with nil channel
	notifyInstallationChange(nil)
}
