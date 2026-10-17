package execute

import (
	"errors"
	"slices"
	"testing"
)

// Releasing runs last acquired first, and reports every failure.
func TestReleasesRunLastAcquiredFirstAndReportEveryFailure(t *testing.T) {
	var order []string
	first, last := errors.New("first failed"), errors.New("last failed")
	var r releases
	r.push(func() error { order = append(order, "workspace"); return first })
	r.push(func() error {
		order = append(order, "read")
		return nil
	})
	r.push(func() error { order = append(order, "encoder"); return last })

	err := r.release()
	if want := []string{"encoder", "read", "workspace"}; !slices.Equal(order, want) {
		t.Errorf("released %v, want %v", order, want)
	}
	if !errors.Is(err, first) || !errors.Is(err, last) {
		t.Errorf("release = %v, want both failures", err)
	}
	if err := r.release(); err != nil || len(order) != 3 {
		t.Errorf("a second release ran something again (%v, %v)", err, order)
	}
}
