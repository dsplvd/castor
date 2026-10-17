package dlna

import (
	"net/http"
	"testing"
)

func TestResponseFeaturesMatchDIDLWithoutInventingAProfile(t *testing.T) {
	for _, features := range []string{"*", "DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO"} {
		meta := `<DIDL-Lite><item><res protocolInfo="http-get:*:video/mp2t:` + features + `">http://example.test/video</res></item></DIDL-Lite>`
		h := http.Header{"Content-Type": {"video/mp2t"}}
		h.Set("transferMode.dlna.org", "Streaming")
		if features != "*" {
			h.Set("contentFeatures.dlna.org", features)
		}
		if problems := streamHeaders(didlContentType(meta), didlFeatures(meta), h); len(problems) != 0 {
			t.Fatal(problems)
		}
		h.Set("contentFeatures.dlna.org", "DLNA.ORG_PN=UNADVERTISED")
		if len(streamHeaders(didlContentType(meta), didlFeatures(meta), h)) == 0 {
			t.Fatal("unadvertised features accepted")
		}
	}
}
