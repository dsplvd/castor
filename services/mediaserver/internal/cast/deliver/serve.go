package deliver

import (
	"context"
	"net"
)

// Listeners chooses the address receivers use to fetch a delivery.
type Listeners interface {
	Listen(context.Context) (net.Listener, error)
}
