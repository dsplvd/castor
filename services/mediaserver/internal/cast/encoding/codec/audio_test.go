package codec

import (
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestSurroundAudioTargets(t *testing.T) {
	aacStereo := media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}
	ac3 := media.AudioSupport{Codec: media.CodecAC3}
	eac3 := media.AudioSupport{Codec: media.CodecEAC3}
	for _, tt := range []struct {
		name     string
		caps     []media.AudioSupport
		codec    media.Codec
		channels int
		want     string
		wantCh   int
	}{
		{"5.1 ac3 the device decodes is copied", []media.AudioSupport{aacStereo, ac3}, media.CodecAC3, 6, "copy", 0},
		{"5.1 aac keeps its layout in ac3", []media.AudioSupport{aacStereo, ac3}, media.CodecAAC, 6, "ac3", 6},
		{"eac3 is preferred when advertised", []media.AudioSupport{eac3, ac3}, "dts", 6, "eac3", 6},
		{"7.1 folds to the 5.1 ceiling", []media.AudioSupport{eac3}, "dts", 8, "eac3", 6},
		{"no surround support downmixes to stereo aac", []media.AudioSupport{aacStereo}, "dts", 6, "aac", 2},
		{"surround codec advertised only in stereo is downmixed", []media.AudioSupport{{Codec: media.CodecEAC3, MaxChannels: 2}}, "dts", 6, "eac3", 2},
		{"mono-only audio support is respected", []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 1}}, "dts", 6, "aac", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var formats []media.FormatSupport
			for _, audio := range tt.caps {
				formats = append(formats, media.FormatSupport{Transport: media.TransportMP4, Audio: audio})
			}
			track := mustDecideAudio(t, Inputs{
				Caps:     media.Capabilities{Formats: formats},
				Probe:    media.ProbeInfo{AudioCodec: tt.codec, AudioChannels: tt.channels},
				Measured: true, Into: testFormat(t, media.MP4),
			})
			enc, _ := track.Encode()
			if track.Name() != tt.want || enc.Channels != tt.wantCh {
				t.Errorf("audio = %s/%dch, want %s/%dch", track.Name(), enc.Channels, tt.want, tt.wantCh)
			}
		})
	}
}

func TestAnUnmeasuredAudioTrackIsNotAnAbsentOne(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	in := Inputs{Caps: caps, Into: testFormat(t, media.MP4), Measured: true}
	if track := mustDecideAudio(t, in); track.Action() != OmitTrack {
		t.Errorf("a probe that measured no audio decided %q, want omission", track.Name())
	}
	in.Measured = false
	if enc, ok := mustDecideAudio(t, in).Encode(); !ok || enc.Codec != media.CodecAAC || enc.Channels != 2 {
		t.Errorf("an unmeasured audio track decided %+v, want the stereo AAC floor", enc)
	}
}

func mustDecideAudio(t *testing.T, in Inputs) Track[AudioEncode] {
	t.Helper()
	track, _, err := decideAudio(in)
	if err != nil {
		t.Fatalf("decideAudio: %v", err)
	}
	return track
}

func testFormat(t *testing.T, contentType string) container.Format {
	t.Helper()
	f, ok := container.For(contentType)
	if !ok {
		t.Fatalf("no producible format for %q", contentType)
	}
	return f
}

// HE-AAC cannot be copied into mp4, but its picture can: the axes are decided apart, so a stream like that keeps its
// video as it is and re-encodes only its sound.
func TestHEAACIsReEncodedForMP4ButItsPictureStaysCopied(t *testing.T) {
	support := func(transport media.Transport) media.FormatSupport {
		return media.FormatSupport{
			Transport: transport,
			Video:     media.VideoSupport{Codec: media.CodecH264},
			Audio:     media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 6},
		}
	}
	aacDevice := media.Capabilities{Formats: []media.FormatSupport{support(media.TransportMP4), support(media.TransportMPEGTS)}}
	probe := func(profile media.Profile) media.ProbeInfo {
		return media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 1080, VideoBitDepth: 8,
			AudioCodec: media.CodecAAC, AudioProfile: profile, AudioChannels: 2, AudioSampleRate: 48000}
	}
	for _, tt := range []struct {
		name      string
		profile   media.Profile
		into      string
		wantCopy  bool
		wantAudio []Reason
	}{
		{"AAC-LC into mp4 is copied", "LC", media.MP4, true, nil},
		{"HE-AAC into mp4 is re-encoded", "HE-AAC", media.MP4, false, []Reason{reasonContainerAudio}},
		{"HE-AACv2 into mp4 is re-encoded", "HE-AACv2", media.MP4, false, []Reason{reasonContainerAudio}},
		{"HE-AAC into mpegts is copied, ADTS keeps its SBR", "HE-AAC", media.MPEGTS, true, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{Caps: aacDevice, Probe: probe(tt.profile), Measured: true, Into: testFormat(t, tt.into), MaxHeight: 1080, Encoders: hostEncoders}
			plan, err := Served(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Video.Action() != CopyTrack {
				t.Errorf("video was not copied: %v", plan.Video.Action())
			}
			if copied := plan.Audio.Action() == CopyTrack; copied != tt.wantCopy {
				t.Errorf("audio copied = %v, want %v (refusals %v)", copied, tt.wantCopy, plan.Refusals)
			}
			if !tt.wantCopy {
				audio, ok := plan.Audio.Encode()
				if !ok || audio.Codec != media.CodecAAC {
					t.Errorf("audio plan = %+v, want an AAC re-encode", audio)
				}
				if !slices.Equal(plan.Refusals, tt.wantAudio) {
					t.Errorf("reasons = %v, want %v", plan.Refusals, tt.wantAudio)
				}
			}
		})
	}
}
