package hls

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

// The shape that silently loses audio: video-only variants, each paired with its own audio group.
func TestParsePlaylistPairsEachVariantWithItsAudioGroup(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud-low",NAME="Low",DEFAULT=YES,URI="audio/low.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud-high",NAME="High",DEFAULT=YES,URI="audio/high.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud-low"` + "\n480.m3u8\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud-high"` + "\n1080.m3u8\n"
	base := sourcetest.URL(t, "http://example.com/master.m3u8")
	master, err := parsePlaylist(body, base, base)
	if err != nil {
		t.Fatal(err)
	}
	rungs := ladder(master, nil, "")
	if len(rungs) != 2 || rungs[0].AudioURL == nil || rungs[1].AudioURL == nil {
		t.Fatalf("ladder = %+v, want two rungs each paired with companion audio", rungs)
	}
	if rungs[0].AudioURL.Path != "/audio/low.m3u8" || rungs[1].AudioURL.Path != "/audio/high.m3u8" {
		t.Errorf("audio = %s / %s, want each rung's own group", rungs[0].AudioURL.Path, rungs[1].AudioURL.Path)
	}
}

func TestParsePlaylistKeepsEachVariantsTextRenditions(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",DEFAULT=YES,URI="subs/en.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Francais",LANGUAGE="fr",FORCED=YES,URI="subs/fr.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480,CODECS="avc1.4d401f,mp4a.40.2",SUBTITLES="subs"` + "\n480.m3u8\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2"` + "\n1080.m3u8\n"
	base := sourcetest.URL(t, "http://example.com/master.m3u8")
	master, err := parsePlaylist(body, base, base)
	if err != nil {
		t.Fatal(err)
	}
	rungs := ladder(master, nil, "")
	if len(rungs) != 2 || len(rungs[0].Captions) != 2 || len(rungs[1].Captions) != 0 {
		t.Fatalf("ladder = %+v, want the first rung alone to carry two text renditions", rungs)
	}
	en, fr := rungs[0].Captions[0], rungs[0].Captions[1]
	if en.URL.Path != "/subs/en.m3u8" || en.Language != "en" || !en.Default || fr.Language != "fr" || !fr.Forced {
		t.Errorf("captions = %+v / %+v", en, fr)
	}
}

func TestParsePlaylistPlaysTheAudioLanguageAskedElseTheDefault(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=YES,URI="audio/en.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Francais",LANGUAGE="fr-FR",URI="audio/fr.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud"` + "\n480.m3u8\n"
	base := sourcetest.URL(t, "http://example.com/master.m3u8")
	master, err := parsePlaylist(body, base, base)
	if err != nil {
		t.Fatal(err)
	}
	for language, want := range map[string]string{"": "/audio/en.m3u8", "fr": "/audio/fr.m3u8", "de": "/audio/en.m3u8"} {
		rungs := ladder(master, nil, language)
		if len(rungs) != 1 || rungs[0].AudioURL == nil || rungs[0].AudioURL.Path != want {
			t.Errorf("language %q: ladder = %+v, want audio %s", language, rungs, want)
		}
	}
}

func TestAudioPreferencesAreTriedWithinEachVariantsGroup(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="low",NAME="French",LANGUAGE="fr",DEFAULT=YES,URI="audio/low-fr.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="high",NAME="German",LANGUAGE="de",DEFAULT=YES,URI="audio/high-de.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="high",NAME="English",LANGUAGE="en",URI="audio/high-en.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="low"` + "\n480.m3u8\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="high"` + "\n1080.m3u8\n"
	base := sourcetest.URL(t, "http://example.com/master.m3u8")
	master, err := parsePlaylist(body, base, base)
	if err != nil {
		t.Fatal(err)
	}
	rungs := ladder(master, nil, "fr", "en")
	if len(rungs) != 2 || rungs[0].AudioURL.Path != "/audio/low-fr.m3u8" || rungs[0].AudioLanguage != "fr" || rungs[1].AudioURL.Path != "/audio/high-en.m3u8" || rungs[1].AudioLanguage != "en" {
		t.Fatalf("preferred audio for each rendition = %+v", rungs)
	}
}
