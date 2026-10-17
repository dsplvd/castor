// Package adapters assembles the output protocols installed by the media server.
package adapters

import (
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver/hls"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/progressive"
)

func Default() output.Factory { return output.Factory{progressive.Adapter{}, hls.Adapter{}} }
