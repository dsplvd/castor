package hls

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// unwrapWebP reads the envelope some origins publish segments in: a valid WebP picture whose
// EXIF chunk holds the MPEG-TS, packet-aligned and uncompressed. A WebP that carries none is
// left alone, since it may be a picture a player is meant to show.
func unwrapWebP(body []byte) ([]byte, bool, error) {
	const header = 12
	if len(body) < header || !bytes.HasPrefix(body, []byte("RIFF")) || string(body[8:12]) != "WEBP" {
		return body, false, nil
	}
	// RIFF chunks: a four-byte name, a little-endian length, the payload padded to an even length.
	for at := header; at+8 <= len(body); {
		name, size := string(body[at:at+4]), uint64(binary.LittleEndian.Uint32(body[at+4:at+8]))
		start := at + 8
		if size > uint64(len(body)-start) {
			// A length that runs past the file: the chunk is the rest of it, as a streamed writer leaves it.
			if name != "EXIF" {
				return body, false, nil
			}
			size = uint64(len(body) - start)
		}
		payload := body[start : start+int(size)]
		if name == "EXIF" && len(payload) > 0 && payload[0] == 0x47 {
			if len(payload) > pixelLimit {
				return nil, false, fmt.Errorf("WebP segment payload exceeds %d bytes", pixelLimit)
			}
			if len(payload) < 3*188 || len(payload)%188 != 0 {
				return nil, false, fmt.Errorf("WebP segment payload is not packet-aligned MPEG-TS")
			}
			for i := 0; i < len(payload); i += 188 {
				if payload[i] != 0x47 {
					return nil, false, fmt.Errorf("WebP segment payload loses MPEG-TS sync at byte %d", i)
				}
			}
			return payload, true, nil
		}
		at = start + int(size) + int(size&1)
	}
	return body, false, nil
}
