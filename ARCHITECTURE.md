# Architecture

Castor ships three independent services in one binary. Clients speak the public API; API calls scraping and media over separate Connect/protobuf contracts.

## Clients and three services

```mermaid
flowchart LR
    ui[TUI and other clients] -- castor.v1 --> api[API server]
    api -- "Page URLs (castor.scraping.v1)" --> scraping[Scraping server]
    scraping -- "Loads webpage and iframes" --> site["Source webpage / player<br/>watch.example"]
    scraping -- "Captures the player's stream requests" --> cdn["Media origin / CDN<br/>video-cdn.example"]
    scraping -- "Stream URLs + replay headers" --> api
    api -- "Stream URLs + preferences (castor.media.v1)" --> media[Media server]
    api -- connects, drives --> device[Device]
    media -- "Fetches manifests + media bytes" --> cdn
    device -- fetches /media --> media
    device -. "Direct playback when handed off" .-> cdn
```

The webpage and media origin are distinct: `watch.example` may load video from `video-cdn.example` (illustrative domains). Scraping discovers stream URLs; media fetches manifests and bytes from those URLs, never the original webpage. Direct playback lets the device fetch the CDN itself.

| Component | Owns | Does not do |
| --- | --- | --- |
| Clients | Catalog, sources, UI; public API calls | Service internals |
| API | Device discovery/control, cast defaults, orchestration, status, cancellation | Browser extraction or media ranking |
| Scraping | Chrome, frames, capture-document parsing; `browser`, `capture` | Devices, probing, ranking, playback |
| Media | Probing, ranking, recovery, ffmpeg, subtitles, `/media`; `resolver`, `whisper` | Webpages or device control |

Scraping emits generated `StreamCandidate` contracts directly, with URLs, replay headers, MIME types, source pages, and manifest evidence. Its private HLS/DASH document inspection identifies referenced resources and pre-rolls from responses Chrome already fetched. API translates public sources into media's page-free `Source`. A direct stream is explicitly chosen and only measured; a candidate list is ranked, with alternatives retained for recovery. Candidates are alternative URLs, not video chunks; one HLS/DASH URL can contain quality variants.

## Topologies

| Setup | What runs where |
| --- | --- |
| Local CLI | Missing services run embedded, with loopback RPC endpoints. |
| Shared media | Run `media-server`; clients set `servers.media.url` and `servers.media.token`. |
| Shared API | Run `api-server` on the devices' network, pointing at media and scraping; clients set `servers.api.url` and `servers.api.token`. |
| Shared scraping | Run `scraping-server` with Chrome; API sets `servers.scraping.url` and `servers.scraping.token`. |

Each standalone command starts only its own service. Standalone API defaults scraping to `http://localhost:8412`. Binary main composes embedded services through callbacks; embedded and remote services use the same HTTP contracts.

## Boundaries

- Services share generated contracts, not implementations; tests fake peers at their contracts.
- Service internals use Go's `internal` directories; only binary main imports service entry points. The TUI stays inside the CLI.
- Consumers declare narrow ports; entry points wire adapters. Decisions use capabilities, not device-family or site branches.
- Shared plumbing handles configuration, HTTP, authentication, watch state, and logging without domain logic.
- Only media needs cgo (Whisper); API and scraping build without it.

## The contracts

Buf generates Go and TypeScript Connect clients from `proto/`. RPC edges validate requests and peer responses with protovalidate; contracts are not stable yet. API resolves optional preferences against its validated `cast` defaults into media's complete `PlaybackSettings`.

- Device types, audio/video codecs, failure codes, and recovery actions are enums.
- Subtitles select disabled, automatic detection, or a language; an absent API selection inherits defaults.
- Status selects connecting, extracting, measuring, or casting with state-specific data. Terminal results select completed, stopped, or a typed failure.
- `Stream` holds playback data; `StreamCandidate` adds discovery evidence. Unknown ranked bitrate is absent, not zero. Media ignores unknown capability enum values rather than assuming support.

| Contract | RPCs |
| --- | --- |
| `castor.v1` (clients → API) | `ListDevices`; `Cast`, `Stop`, `ListCasts`; `Watch` (status, final `Ended`); `Resolve` (preview, best first). |
| `castor.scraping.v1` (API → scraping) | `ScrapingService.Resolve`: HTTP(S) pages → unranked candidates. Never calls media. |
| `castor.media.v1` (API → media) | `CastService.Start`, `Stop`, `Watch`; `StreamService.Rank`; `DeviceService.Drive` (capabilities and command stream), `Answer` (command results). |

## A cast, start to end

1. API mints the cast ID and returns it, connects the device (`CONNECTING`), then resolves pages through scraping (`EXTRACTING`). Direct streams bypass scraping.
2. API starts media under that same ID with streams and complete preferences (media refuses an ID already in use), opens `Watch` and `Drive`, and lends the device's capabilities. Media measures/ranks (`MEASURING`), chooses delivery, then plays (`CASTING`). A cast without a lent device is abandoned after a minute.
3. Media sends device commands down `Drive`; API executes them and returns `Answer`. Media decides playback and recovery without needing access to the device's network.
4. API forwards status to watchers, ending with `Ended`. The cast belongs to API, not the UI; the CLI chooses to stop it on Ctrl+C. Media can continue after its lender leaves until it needs device control again.

Every call a cast makes carries its ID in the `Castor-Cast-Id` header, and every log line logged under a cast gets `cast_id`, in all three services and in the CLI, so one ID follows a cast through the logs.

Stopping during extraction cancels scraping and releases the device without creating a media cast. Once media starts, API reconciles stop requests and waits for remote completion within a bounded grace period, then joins its Watch and Drive workers before releasing the device. Shutdown drains each service's own work.

Four small state machines validate cast progress, attempt execution, playback health, and the API's remote session. Each owner accepts its own typed events. Callbacks record evidence and diagnostics; the owners run operations and join workers. An attempt joins producers and active delivery requests before releasing storage, and recovery waits for that cleanup before starting another attempt.

## Inside the media engine

```mermaid
flowchart LR
    candidates[Resolved candidates] --> rank[Measure and rank] --> compose{Compose}
    compose -- hand off --> fetch[Device fetches the media origin / CDN]
    compose -- serve --> plan[Plan] --> deliver[Deliver] --> health{Health}
    health -- fault --> compose
```

- **Rank:** prefer the tallest stream within `cast.max_height`; unmeasured candidates are last resorts. Media's HLS/DASH readers handle playback, not capture-document parsing.
- **Compose:** hand off compatible sources to self-fetching devices unless `delivery: serve`. For HLS, the chosen playlist must have segment names the receiver can fetch, and a probe that decoded TIKTIKPX PNG/gzip/TS segments requires receiver support for that envelope. A later live segment can differ from the observed ones; a receiver refusal can trigger a served retry. Other sources are remuxed or use a read-once encoding pipeline (e.g. DLNA) as needed. `--debug` explains the choice.
- **Plan/deliver:** copy or encode each track, scale to the ceiling, and show the source's own captions as a native track when it publishes the language asked, else burn Whisper subtitles into read-once casts. Use a working GPU encoder, falling back to software. Deliveries register handlers under random paths on the media server's shared `/media` route. Closing a publication removes its route and joins active requests before its storage is released.
- **Recover:** detect stalls, slow reads, or stopped fetching; revise the attempt, cheapest first, or try the next candidate.

## Trust boundary

On Unix platforms (including Android), FFmpeg progress and PCM side outputs use anonymous pipes inherited through `exec.Cmd.ExtraFiles`. The parent closes its copies of the write ends after process startup and drains the read ends through child exit. Windows uses local named pipes restricted to the process user, opened by FFmpeg through its file protocol.

Services use independent bearer tokens; health checks and device-facing `/media` are open. Invalid downstream credentials are API misconfiguration, not caller authentication failures. Replay headers reach media but are stripped from public listings. See [SECURITY.md](SECURITY.md).
