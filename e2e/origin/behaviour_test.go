package origin_test

import (
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/dash"
	"net/http/httptest"
	"testing"
)

func TestDASHInitializationIsNotAMediaSegment(t *testing.T) {
	out := (dash.Packager{}).Package(t.TempDir(), origin.Layout{Rungs: 1})
	p := origin.Published{SegmentExt: out.SegmentExt, InitPrefix: out.InitPrefix}
	for raw, want := range map[string]bool{"/init-0.m4s": false, "/chunk-0-00001.m4s": true, "/manifest.mpd": false} {
		if p.IsSegment(httptest.NewRequest("GET", raw, nil)) != want {
			t.Errorf("segment classification %s", raw)
		}
	}
}
