package extract

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestChatAndSocialFramesAreNotPlayers(t *testing.T) {
	for src, want := range map[string]bool{
		"https://timstreams-01.chatango.com/":                   true,
		"https://st.chatango.com/h5/gz/r0501261539/id.html":     true,
		"https://chatango.com/":                                 true,
		"https://disqus.com/embed/comments/?base=default":       true,
		"https://tlk.io/some-room":                              true,
		"https://minnit.chat/room?embed":                        true,
		"https://e.widgetbot.io/channels/1/2":                   true,
		"https://discord.com/widget?id=1":                       true,
		"https://www.twitch.tv/embed/someone/chat?parent=x.com": true,
		"https://www.youtube.com/live_chat?v=abc":               true,
		"https://site.example/embed/chat/9":                     true,
		"https://site.example/comments":                         true,
		"https://ehgembxe.cfd/n58ajgfx-6006":                    false,
		"https://player.twitch.tv/?channel=someone":             false,
		"https://www.youtube.com/embed/abc":                     false,
		"https://notchatango.com/play/1":                        false,
		"https://site.example/play/chatter":                     false,
		"https://site.example/embed?chat=1":                     false,
	} {
		if got := chatFrame(src); got != want {
			t.Errorf("chatFrame(%q) = %v, want %v", src, got, want)
		}
	}
}

// framedPage is a page with the given frames in it, on an origin that serves /live.m3u8 and /player as player.
func framedPage(t *testing.T, frames string, player http.HandlerFunc) *session {
	t.Helper()
	counter := &hitCounter{}
	return sourcePage(t, `<body style="margin:0">`+frames+`</body>`, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/player" && player != nil {
			player(w, r)
			return
		}
		counter.handler(w, r)
	})
}

const playerEmbed = `<iframe src="/player" allow="autoplay; encrypted-media; fullscreen" style="width:640px;height:360px"></iframe>`

func TestAChatFrameIsNotFollowedAsAPlayer(t *testing.T) {
	s := framedPage(t, `<iframe src="/chat/room" style="width:900px;height:600px"></iframe>
<iframe src="/player" style="width:640px;height:360px"></iframe>`, nil)
	frames, err := findFrames(s.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || !strings.HasSuffix(frames[0].Src, "/player") {
		t.Errorf("frames = %v, want only the player, though the chat is larger", frames)
	}
}

func TestOnlyAFrameBuiltLikeAPlayerIsTakenForOne(t *testing.T) {
	s := framedPage(t, `<iframe src="/chat/room" allow="autoplay" style="width:640px;height:360px"></iframe>
<iframe src="/banner" style="width:300px;height:250px"></iframe>
<iframe src="/tiny" allow="autoplay" style="width:150px;height:100px"></iframe>
<iframe src="/hidden" allow="autoplay" style="display:none;width:640px;height:360px"></iframe>
<iframe src="/permitted" allow="autoplay" style="width:480px;height:270px"></iframe>
<iframe src="/large" style="width:800px;height:450px"></iframe>`, nil)
	frames, err := playerFrames(s.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range frames {
		got = append(got, strings.TrimPrefix(f.Src[strings.LastIndex(f.Src, "/"):], "/"))
	}
	if want := "large,permitted"; strings.Join(got, ",") != want {
		t.Errorf("player frames %q, want %q: no chat, banner, small or hidden frame, largest first", got, want)
	}
}

func TestTheWaitForSourcesEndsEarlyOnceAPlayerFrameIsUp(t *testing.T) {
	s := framedPage(t, playerEmbed, nil)
	start := time.Now()
	if err := selectSources(s.ctx, s.collector.hasStream, time.Second, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > sourcePanelWaitWithPlayer+2*time.Second {
		t.Errorf("a page with a player frame cost %s, want about %s", elapsed, sourcePanelWaitWithPlayer)
	}
}

func TestTheWaitForSourcesLastsWhenOnlyAChatFrameIsUp(t *testing.T) {
	s := framedPage(t, `<iframe src="/chat/room" allow="autoplay" style="width:640px;height:360px"></iframe>`, nil)
	wait := sourcePanelWaitWithPlayer + 2*time.Second
	start := time.Now()
	if err := selectSources(s.ctx, s.collector.hasStream, time.Second, wait); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < wait-500*time.Millisecond {
		t.Errorf("a page with only a chat frame cost %s, want the whole wait of %s", elapsed, wait)
	}
}

// refusesToPlayOnTop is a player that fetches a stream on its second click, and only while it is framed.
func refusesToPlayOnTop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<body style="margin:0;width:100%;height:100%"><script>
let clicks = 0;
document.addEventListener('click', () => {
  if (++clicks >= 2 && window.top !== window.self) fetch('/live.m3u8');
});
</script></body>`)
}

func TestAPlayerFrameThatRefusesToPlayOnTopIsClickedWhereItSits(t *testing.T) {
	s := framedPage(t, playerEmbed, refusesToPlayOnTop)
	if err := clickPlayerFrame(s.ctx, s.collector.hasStream, 6*time.Second); err != nil {
		t.Fatal(err)
	}
	if !s.collector.hasStream() {
		t.Fatal("no stream was captured from the framed player")
	}
	var at string
	if err := chromedp.Run(s.ctx, chromedp.Location(&at)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(at, "/page") {
		t.Errorf("the page is at %q, want it left where it was", at)
	}
}

func TestAPlayerFrameThatFetchesNothingIsGivenUpOnAfterTwoClicks(t *testing.T) {
	s := framedPage(t, playerEmbed, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<body><script>document.addEventListener('click', () => fetch('/hit/click'))</script></body>`)
	})
	start := time.Now()
	if err := clickPlayerFrame(s.ctx, s.collector.hasStream, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if s.collector.hasStream() {
		t.Error("a stream was captured from a player that fetches none")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("clicking the frame cost %s, want no more than its settle window", elapsed)
	}
}

func TestAPageWithNoPlayerFrameIsNotClicked(t *testing.T) {
	s := framedPage(t, `<button onclick="fetch('/live.m3u8')" style="width:600px;height:400px">Play</button>`, nil)
	if err := clickPlayerFrame(s.ctx, s.collector.hasStream, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if s.collector.hasStream() {
		t.Error("clicking the frame reached something that is not one")
	}
}
