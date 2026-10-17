// Package deliver owns bounded media storage, progressive HTTP delivery and the
// neutral opening and readiness types shared by output adapters.
package deliver

import (
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
)

// Artifact is what a delivery hands the device, which the watch that opens it waits on.
type Artifact struct {
	// Path names the local artifact for probing without issuing receiver requests.
	Path string
	// Subject names it in a log line and in a fault ("the stream output", "the HLS playlist").
	Subject string

	Landed func() int64

	Grace time.Duration
}

// Opening is everything a delivery mechanism is opened with.
type Opening struct {
	// Abort stops the owning producer when a storage guard fails.
	Abort     func()
	Live      bool
	Format    container.Format
	Listeners Listeners
	Headers   map[string]string

	// IdleGrace is how long an idle device is waited for before the delivery counts as done.
	IdleGrace time.Duration

	// WriteDeadline bounds one socket write, so a hung client cannot hold its goroutine.
	WriteDeadline time.Duration
}
