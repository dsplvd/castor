# Castor for Android

A remote control for a castor API server: pick a TV, cast a page or a stream URL, watch its state, stop it.
Kotlin, Jetpack Compose (Material 3), connect-kotlin on OkHttp (Connect protocol, bearer token).

## Build and install

```sh
export ANDROID_HOME=$HOME/Library/Android/sdk   # and a JDK 17
echo "sdk.dir=$ANDROID_HOME" > local.properties # gitignored
./gradlew :app:assembleDebug :app:testDebugUnitTest
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

## Regenerate the protobuf and Connect code

`app/src/main/gen/` is generated from `../proto/castor/v1` and committed; never edit it by hand.
Run from `android/` after changing `proto/` (needs network for the Buf remote plugins):

```sh
go run github.com/bufbuild/buf/cmd/buf@v1.50.0 generate --template buf.gen.yaml
```

The generated Java targets protobuf-javalite 4.36.2; keep `protobuf` in `gradle/libs.versions.toml`
at least that version when the plugins are updated (see the header of the generated files).

## Use

1. Settings: server URL (e.g. `http://192.168.50.32:8411`), token, optional max height and delivery; "Test connection" calls ListDevices.
2. Devices: pull to refresh, tap one to make it the default, or pin a type and address for a TV the server cannot discover.
3. Cast: paste a URL, choose page (server extracts the stream) or direct stream URL, Cast.
   Share a page from Chrome to Castor to open this screen prefilled.
4. Now Playing follows the cast; the stream drops in the background and reconnects on return.

Opening http(s) links with Castor is off by default. To enable it:
`adb shell pm enable dev.castor.android/.ViewLinksAlias`.

Cleartext HTTP is allowed (see `res/xml/network_security_config.xml`) because the server is on the LAN without TLS.

## Browser capture

Cast tab, "Open in browser" (or share a page to Castor and choose "Open in browser") opens an in-app browser:
a plain WebView with the default user agent, JavaScript and DOM storage on, third-party cookies accepted
(players often live in cross-origin iframes), popups and non-http(s) schemes (`intent://`, `market://`) ignored.
You play the video yourself, dismissing popups and solving any challenge as a normal viewer.

How it works: `shouldInterceptRequest` (and the service-worker client) only observes requests and always
lets the WebView load them. A request is a candidate when its URL path ends in `.m3u8`/`.mpd` (HLS/DASH,
the primary candidates) or `.mp4`/`.webm`, or when its Accept header names a manifest type; query strings are
ignored for matching but the exact URL is kept (signed tokens). `.ts`/`.m4s` segments are only evidence that a
playlist on that host is active. Known ad/analytics hosts, `/ads/`, `/vast`, `/vpaid` paths and `blob:`/`data:`
URLs are skipped. The "N streams caught" chip opens a list (default selection: all manifests, most active
first); Cast sends the selection as `Source.streams` to the default device with your Preferences, then opens
Now Playing. With nothing caught, "Let the server extract it" sends the current page as `Source.pages`.
The server measures, ranks and plays the candidates.

What is sent to your castor server, per stream (at most 20): the URL; the request headers the WebView
exposes (Referer, Origin, User-Agent, Accept, ...; never Range, Host, Content-Length, Connection,
Accept-Encoding and similar, nor invalid ones; at most 64); the `Cookie` for that URL from the WebView's cookie jar,
so cookies for the stream host leave the phone for your own castor server; the page URL as `source_page`;
and a `content_type` only when the extension makes it obvious. Leaving the screen after casting pauses the
page, and "Mute page" mutes media in the top page.

Details: when the list opens, the phone fetches each HLS/DASH manifest itself, with the headers and cookies
the page used for it (at most 4 at a time, 8 s timeouts, 2 MB), and shows a second line such as
"1080p · 6.5 Mb/s · H.264/AAC · live": the best quality, its bitrate and codecs, and live or VOD ("N qualities"
lists them all). For an HLS master playlist the first quality's media playlist is fetched too, to learn live/VOD
and encryption. A red "DRM: Widevine" (or PlayReady, FairPlay, "encrypted (CENC)") badge marks streams castor
cannot play; they sort last and are not selected by default. "AES-128" is plain encryption castor can usually play.
A failed fetch shows its reason (for example "HTTP 403"); that only means the phone could not read it, the server may still.

Limits:
- Media fetched by the WebView's native `<video>` element may not pass through `shouldInterceptRequest`, so
  some players show no streams. Only requests and their headers are visible, never responses or content types,
  so detection is by URL.
- The server fetches from its own IP: fine on the same home network, but IP-locked links fail elsewhere.
- A LAN DNS filter (Pi-hole, AdGuard) can block a player's stream host on the phone too, so the player never
  starts and nothing is caught.
- "Mute page" cannot reach players inside cross-origin frames.
