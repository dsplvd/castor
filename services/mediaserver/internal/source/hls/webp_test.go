package hls

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

func riffChunk(name string, payload []byte) []byte {
	out := append([]byte(name), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func webp(chunks ...[]byte) []byte {
	body := append([]byte("WEBP"), bytes.Join(chunks, nil)...)
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func transportStream(packets int) []byte {
	ts := make([]byte, packets*188)
	for i := 0; i < len(ts); i += 188 {
		ts[i] = 0x47
	}
	return ts
}

func TestWebPSegments(t *testing.T) {
	ts := transportStream(4)
	picture := riffChunk("VP8L", []byte{0x2f, 0, 0, 0, 0x10, 7, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 7})
	metadata := riffChunk("EXIF", []byte("Exif\x00\x00II*\x00"))
	cutShort := append(webp(picture), append([]byte("EXIF"), append([]byte{0xff, 0xff, 0xff, 0x7f}, ts...)...)...)
	for name, tc := range map[string]struct {
		body []byte
		want []byte
		ok   bool
		err  bool
	}{
		"the transport stream in the EXIF chunk": {body: webp(picture, riffChunk("EXIF", ts)), want: ts, ok: true},
		"an odd-length chunk before it":          {body: webp(riffChunk("ICCP", []byte{1, 2, 3}), riffChunk("EXIF", ts)), want: ts, ok: true},
		"a length the file cuts short":           {body: cutShort, want: ts, ok: true},
		"an ordinary picture":                    {body: webp(picture), want: webp(picture)},
		"EXIF metadata that is not a stream":     {body: webp(picture, metadata), want: webp(picture, metadata)},
		"not a picture":                          {body: ts, want: ts},
		"a stream that loses sync":               {body: webp(riffChunk("EXIF", append(bytes.Clone(ts[:188]), make([]byte, 3*188)...))), err: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok, err := unwrapWebP(tc.body)
			if (err != nil) != tc.err || ok != tc.ok || (!tc.err && !bytes.Equal(got, tc.want)) {
				t.Fatalf("unwrapWebP = (%d bytes, %v, %v), want (%d bytes, %v, error %v)", len(got), ok, err, len(tc.want), tc.ok, tc.err)
			}
		})
	}
}

func TestDecoderUnwrapsAWebPSegment(t *testing.T) {
	ts := transportStream(4)
	got, ok, err := (Decoder{}).Segment(t.Context(), timeline.Segment{URI: "segment"}, webp(riffChunk("EXIF", ts)), nil)
	if err != nil || !ok || !bytes.Equal(got, ts) {
		t.Fatalf("Segment = (%d bytes, %v, %v), want the transport stream", len(got), ok, err)
	}
}
