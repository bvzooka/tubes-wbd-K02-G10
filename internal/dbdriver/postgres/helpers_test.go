package postgres

import (
	"testing"

	"dbplatform/internal/testutil"
)

func newBlankDriver(t *testing.T, initSQL ...string) *Driver {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	return NewFromPool(testutil.NewTargetDB(t, initSQL...))
}
