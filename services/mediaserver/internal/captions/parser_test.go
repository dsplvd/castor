package captions

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestNonCueBlocksCannotSuppressFallback(t *testing.T) {
	for _, block := range []string{"NOTE a comment", "NOTE\tcomment", "STYLE", "REGION"} {
		t.Run(block, func(t *testing.T) {
			client := pages{"/track.vtt": "WEBVTT\n\n" + block + "\n00:00:01.000 --> 00:00:02.000\nnot a cue\n"}
			_, err := FetchAt(t.Context(), client, source.Caption{URL: &url.URL{Scheme: "http", Host: "source.test", Path: "/track.vtt"}}, nil, nil)
			if err == nil {
				t.Fatal("non-cue content accepted as captions")
			}
		})
	}
}
func TestHeaderMustBeCompleteSignature(t *testing.T) {
	for _, header := range []string{"WEBVTTgarbage", "#EXTM3Ugarbage"} {
		client := pages{"/track": header + "\n\n00:00:01.000 --> 00:00:02.000\nhello\n"}
		if _, err := FetchAt(t.Context(), client, source.Caption{URL: &url.URL{Path: "/track"}}, nil, nil); err == nil {
			t.Fatalf("accepted header %q", header)
		}
	}
	for _, header := range []string{"WEBVTT description", "WEBVTT\tdescription", "\ufeffWEBVTT\r\n"} {
		if !documentHeader(header, "WEBVTT") {
			t.Fatalf("valid header rejected: %q", header)
		}
	}
}
func TestCueParsingHonorsHorizontalWhitespace(t *testing.T) {
	cues, _, _ := parse("WEBVTT\n\n00:00:01.000\t-->\t00:00:02.000\talign:start\nhello\n")
	if len(cues) != 1 || cues[0].settings != " align:start" {
		t.Fatalf("cues=%v", cues)
	}
}
func TestFractionalHoursAndMinutesAreRejected(t *testing.T) {
	for _, value := range []string{"1.5:00:00.000", "00:1.5:00.000", "1.5:00.000"} {
		if _, ok := parseTime(value); ok {
			t.Fatalf("accepted %q", value)
		}
	}
}
func TestMergeDeduplicatesOverlappingCuesAcrossSegments(t *testing.T) {
	a := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nfirst\n\n00:00:01.000 --> 00:00:03.000\nsecond\n"
	b := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nfirst\n"
	got := merge([]string{a, b}, 0)
	if strings.Count(got, "first") != 1 || strings.Count(got, "second") != 1 {
		t.Fatalf("duplicate or missing overlapping cues: %s", got)
	}
}
func TestCueTextCannotChangeTimestampMap(t *testing.T) {
	_, _, mapped := parse("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:90000\n")
	if mapped {
		t.Fatal("cue text interpreted as header metadata")
	}
}

func TestLiteralTimestampMapInCueTextIsNotInvalidHeader(t *testing.T) {
	client := pages{"/track": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nX-TIMESTAMP-MAP is a header field\n"}
	if _, err := FetchAt(t.Context(), client, source.Caption{URL: &url.URL{Path: "/track"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCaptionDocumentAcceptsCRLineEndings(t *testing.T) {
	client := pages{"/track": "WEBVTT\r\r00:00:01.000 --> 00:00:02.000\rhello\r"}
	if _, err := FetchAt(t.Context(), client, source.Caption{URL: &url.URL{Path: "/track"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
}
