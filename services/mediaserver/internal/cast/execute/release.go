package execute

import (
	"errors"
	"slices"
)

// releases belongs to the pipeline goroutine. Acquisitions finish before teardown;
// cleanup runs last acquired first, joining workers before their storage goes.
type releases struct {
	stack []func() error
}

func (r *releases) push(release func() error) {
	r.stack = append(r.stack, release)
}

func (r *releases) release() error {
	stack := r.stack
	r.stack = nil
	var errs []error
	for _, release := range slices.Backward(stack) {
		errs = append(errs, release())
	}
	return errors.Join(errs...)
}
