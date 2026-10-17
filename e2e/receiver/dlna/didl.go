package dlna

import (
	"encoding/xml"
	"strings"
)

// didlContentType reads the MIME the DIDL-Lite res declares (http-get:*:<mime>:<features>).
func didlContentType(meta string) string {
	var didl struct {
		Res []struct {
			ProtocolInfo string `xml:"protocolInfo,attr"`
		} `xml:"item>res"`
	}
	if err := xml.Unmarshal([]byte(meta), &didl); err != nil {
		return ""
	}
	if len(didl.Res) == 0 {
		return ""
	}
	// The first res is the media; later ones are companions such as captions.
	fields := strings.SplitN(didl.Res[0].ProtocolInfo, ":", 4)
	if len(fields) < 3 {
		return ""
	}
	return fields[2]
}

// didlCaptions are the URLs of the text tracks the DIDL-Lite offers beside the media.
func didlCaptions(meta string) []string {
	var didl struct {
		Res []struct {
			ProtocolInfo string `xml:"protocolInfo,attr"`
			URL          string `xml:",chardata"`
		} `xml:"item>res"`
	}
	if err := xml.Unmarshal([]byte(meta), &didl); err != nil {
		return nil
	}
	var urls []string
	for _, res := range didl.Res {
		if strings.Contains(res.ProtocolInfo, ":text/srt:") {
			urls = append(urls, res.URL)
		}
	}
	return urls
}
