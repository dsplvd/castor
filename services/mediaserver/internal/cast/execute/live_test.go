package execute

import (
	"net/url"
	"testing"
)

// A device that tells a stream with no end from a file is told which it is handed.
func TestAHandToTheDeviceSaysWhetherTheStreamHasNoEnd(t *testing.T) {
	target := &url.URL{Scheme: "http", Host: "media.test", Path: "/stream.mp4"}
	for _, live := range []bool{true, false} {
		dev := &fakeDevice{}
		if err := hand(t.Context(), dev, target, "video/mp4", nil, false, live); err != nil {
			t.Fatal(err)
		}
		if dev.live != live {
			t.Errorf("the device was handed live=%v, want %v", dev.live, live)
		}
	}
}
