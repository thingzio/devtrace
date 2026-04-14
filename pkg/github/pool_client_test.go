package github

import "testing"

func TestPoolClientImplementsInterface(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("test-token")
	var _ Client = NewPoolClient(pool)
}

func TestPoolClientEmptyPool(t *testing.T) {
	t.Parallel()
	pool := NewTokenPool("")
	client := NewPoolClient(pool)

	_, err := client.FetchUser(t.Context(), "testuser")
	if err == nil {
		t.Error("expected error with empty pool")
	}

	_, err = client.FetchSignals(t.Context(), "testuser", "", nil)
	if err == nil {
		t.Error("expected error with empty pool")
	}
}

func TestIsRateLimited(t *testing.T) {
	t.Parallel()

	if isRateLimited(nil) {
		t.Error("nil should not be rate limited")
	}

	if isRateLimited(errForTest("some error")) {
		t.Error("generic error should not be rate limited")
	}
}

type errForTest string

func (e errForTest) Error() string { return string(e) }
