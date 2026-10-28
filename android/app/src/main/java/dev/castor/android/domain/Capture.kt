package dev.castor.android.domain

import com.castor.v1.CastRequest
import com.castor.v1.Ladder
import com.castor.v1.Source
import com.castor.v1.Stream
import com.castor.v1.StreamCandidate
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** What a captured request looks like it carries. Detection is by URL and Accept hint only. */
enum class MediaKind(val label: String, val contentType: String, val manifest: Boolean) {
    HLS("HLS", "application/vnd.apple.mpegurl", true),
    DASH("DASH", "application/dash+xml", true),
    MP4("MP4", "video/mp4", false),
    WEBM("WebM", "video/webm", false),
    /** Looks like video from the Accept header alone; the server identifies it. */
    VIDEO("Video", "", false),
}

sealed interface UrlClass {
    /** Could be what the user wants to cast. */
    data class Candidate(val kind: MediaKind) : UrlClass
    /** Segments: proof that a playlist on the same host is playing, never cast on their own. */
    data object Evidence : UrlClass
    data object Ignored : UrlClass
}

const val MAX_URL_LEN = 8192
const val MAX_HEADERS = 64
const val MAX_SENT_STREAMS = 20
const val MAX_KEPT_CANDIDATES = 200

private val adHosts = listOf(
    "doubleclick.net", "googlesyndication.com", "googleadservices.com", "google-analytics.com",
    "googletagmanager.com", "googletagservices.com", "adservice.google.com", "imasdk.googleapis.com",
    "adnxs.com", "adsrvr.org", "taboola.com", "outbrain.com", "scorecardresearch.com",
    "moatads.com", "popads.net", "exoclick.com", "juicyads.com", "trafficjunky.com",
    "hotjar.com", "segment.io", "mixpanel.com", "branch.io",
)
private val adSegments = setOf("ads", "ad", "adserver", "adserve", "vast", "vpaid", "preroll")
private val manifestExt = mapOf("m3u8" to MediaKind.HLS, "mpd" to MediaKind.DASH)
private val fileExt = mapOf("mp4" to MediaKind.MP4, "m4v" to MediaKind.MP4, "webm" to MediaKind.WEBM)
private val segmentExt = setOf("m4s", "ts", "cmfv", "cmfa", "aac", "m4a")

fun isAdHost(host: String): Boolean {
    val h = host.lowercase().trimEnd('.')
    return adHosts.any { h == it || h.endsWith(".$it") }
}

private fun hasAdPath(url: HttpUrl): Boolean = url.pathSegments.any { seg ->
    val s = seg.lowercase()
    s in adSegments || s.startsWith("vast") || s.startsWith("vpaid")
}

/**
 * Sorts a request URL into candidate / evidence / ignored. Query strings are ignored for the
 * extension; [accept] (the request's Accept header) only decides when the path says nothing.
 */
fun classifyUrl(url: String, accept: String? = null): UrlClass {
    if (url.length > MAX_URL_LEN) return UrlClass.Ignored
    val u = url.toHttpUrlOrNull() ?: return UrlClass.Ignored // blob:, data:, ws:, junk
    if (isAdHost(u.host) || hasAdPath(u)) return UrlClass.Ignored
    val last = u.pathSegments.last().lowercase()
    val ext = last.substringAfterLast('.', "")
    manifestExt[ext]?.let { return UrlClass.Candidate(it) }
    if (ext in segmentExt) return UrlClass.Evidence
    fileExt[ext]?.let { return UrlClass.Candidate(it) }
    val a = accept?.lowercase().orEmpty()
    return when {
        "mpegurl" in a -> UrlClass.Candidate(MediaKind.HLS)
        "dash+xml" in a -> UrlClass.Candidate(MediaKind.DASH)
        a.trimStart().startsWith("video/") && ext.isEmpty() -> UrlClass.Candidate(MediaKind.VIDEO)
        else -> UrlClass.Ignored
    }
}

// ---------------------------------------------------------------- headers

/** Headers the server must not replay: it sets its own, or they are about this one request. */
private val droppedHeaders = setOf(
    "range", "content-length", "host", "connection", "accept-encoding", "if-range",
    "if-none-match", "if-modified-since", "transfer-encoding", "te", "upgrade", "keep-alive",
    "content-type", "expect", "x-requested-with",
)
private val headerName = Regex("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

fun isValidHeaderName(name: String) = headerName.matches(name)

/** HTTP field value: no CR/LF/NUL or other controls (tab allowed), and no surrounding blanks. */
fun isValidHeaderValue(value: String) = value.none { (it < ' ' && it != '\t') || it == '\u007f' }

/** Merges two Cookie header strings; [fresh] wins on a repeated cookie name. */
fun mergeCookies(existing: String?, fresh: String?): String {
    val out = LinkedHashMap<String, String>()
    for (src in listOf(existing, fresh)) {
        src.orEmpty().split(';').map { it.trim() }.filter { it.isNotEmpty() }.forEach { pair ->
            val name = pair.substringBefore('=').trim()
            if (name.isNotEmpty()) out[name] = pair
        }
    }
    return out.values.joinToString("; ")
}

private val keepFirst = listOf("referer", "origin", "user-agent", "cookie", "accept", "accept-language", "authorization")

/**
 * The headers worth replaying: valid, not in the dropped set, [cookie] merged into Cookie,
 * at most [MAX_HEADERS], the ones that usually matter first. Names are deduped case-insensitively.
 */
fun sanitizeHeaders(raw: Map<String, String>, cookie: String? = null): Map<String, String> {
    val byLower = LinkedHashMap<String, Pair<String, String>>()
    for ((n, v0) in raw) {
        val name = n.trim()
        val value = v0.trim()
        if (!isValidHeaderName(name) || !isValidHeaderValue(value)) continue
        val lower = name.lowercase()
        if (lower in droppedHeaders) continue
        byLower[lower] = name to value
    }
    val merged = mergeCookies(byLower["cookie"]?.second, cookie)
    if (merged.isNotEmpty() && isValidHeaderValue(merged)) byLower["cookie"] = "Cookie" to merged
    val ordered = keepFirst.mapNotNull { byLower[it] } +
        byLower.filterKeys { it !in keepFirst }.values
    return LinkedHashMap<String, String>().also { m ->
        ordered.take(MAX_HEADERS).forEach { (n, v) -> m[n] = v }
    }
}

// ---------------------------------------------------------------- captured candidates

data class CapturedStream(
    val url: String,
    val kind: MediaKind,
    val headers: Map<String, String>,
    val sourcePage: String,
    val fromFrame: Boolean,
    val firstSeenMs: Long,
    val lastSeenMs: Long,
    val hits: Int,
    /** Segment requests seen on the same host: the stream is actually playing. */
    val activity: Int = 0,
) {
    val host: String get() = url.toHttpUrlOrNull()?.host ?: ""
    val path: String get() = url.toHttpUrlOrNull()?.encodedPath ?: ""
    val groupKey: String get() = host + path
    val pathTail: String get() = path.trimEnd('/').substringAfterLast('/').ifEmpty { path }
}

/** A request as the WebView showed it, before judging. */
class RawRequest(
    val url: String,
    val headers: Map<String, String>,
    val cookie: String?,
    val pageUrl: String,
    val isMainFrame: Boolean,
)

/** Thread-safe: shouldInterceptRequest calls [record] from WebView's background threads. */
class CaptureLog(private val clock: () -> Long = System::currentTimeMillis) {
    private val lock = Any()
    private val byUrl = LinkedHashMap<String, CapturedStream>()
    private val evidenceByHost = HashMap<String, Int>()

    /** Returns true when the candidate list changed (a new URL or another hit). */
    fun record(r: RawRequest): Boolean {
        val accept = r.headers.entries.firstOrNull { it.key.equals("accept", true) }?.value
        val now = clock()
        return when (val c = classifyUrl(r.url, accept)) {
            UrlClass.Ignored -> false
            UrlClass.Evidence -> {
                val host = r.url.toHttpUrlOrNull()?.host ?: return false
                synchronized(lock) { evidenceByHost.merge(host, 1, Int::plus) }
                false
            }
            is UrlClass.Candidate -> synchronized(lock) {
                val old = byUrl[r.url]
                byUrl[r.url] = old?.copy(lastSeenMs = now, hits = old.hits + 1)
                    ?: CapturedStream(
                        url = r.url,
                        kind = c.kind,
                        headers = sanitizeHeaders(r.headers, r.cookie),
                        sourcePage = r.pageUrl.takeIf { isHttpUrl(it) && it.length <= MAX_URL_LEN }.orEmpty(),
                        fromFrame = !r.isMainFrame,
                        firstSeenMs = now,
                        lastSeenMs = now,
                        hits = 1,
                    )
                if (byUrl.size > MAX_KEPT_CANDIDATES) {
                    byUrl.values.minWithOrNull(compareBy({ it.hits }, { it.lastSeenMs }))?.let { byUrl.remove(it.url) }
                }
                true
            }
        }
    }

    /** Candidates ranked best first, with the host's segment activity folded in. */
    fun snapshot(): List<CapturedStream> = synchronized(lock) {
        rank(byUrl.values.map { it.copy(activity = evidenceByHost[it.host] ?: 0) })
    }

    fun clear() = synchronized(lock) { byUrl.clear(); evidenceByHost.clear() }
}

/** Manifests first, then by activity (hits plus segments on the host), then newest. */
fun rank(items: List<CapturedStream>): List<CapturedStream> =
    items.sortedWith(
        compareByDescending<CapturedStream> { it.kind.manifest }
            .thenByDescending { it.hits + it.activity }
            .thenByDescending { it.lastSeenMs },
    )

/** Candidates grouped by host+path (signed URLs of one resource sit together), best group first. */
fun groupCandidates(ranked: List<CapturedStream>): List<List<CapturedStream>> =
    ranked.groupBy { it.groupKey }.values.toList()

/**
 * What is selected until the user touches the list: every manifest, or everything if none.
 * URLs in [drm] are left out, since castor cannot play them.
 */
fun defaultSelection(ranked: List<CapturedStream>, drm: Set<String> = emptySet()): Set<String> {
    val open = ranked.filter { it.url !in drm }
    val manifests = open.filter { it.kind.manifest }
    return (manifests.ifEmpty { open }).map { it.url }.toSet()
}

// ---------------------------------------------------------------- cast request

/** Source.streams from the chosen candidates: best [MAX_SENT_STREAMS] in ranked order. */
fun buildStreamsCastRequest(
    chosen: List<CapturedStream>,
    target: TargetChoice,
    maxHeight: Int? = null,
    delivery: DeliveryPref = DeliveryPref.DEFAULT,
): CastRequest {
    val streams = Source.ResolvedStreams.newBuilder()
    rank(chosen).distinctBy { it.url }.filter { it.url.length <= MAX_URL_LEN }.take(MAX_SENT_STREAMS).forEach { c ->
        val stream = Stream.newBuilder().setUrl(c.url).setContentType(c.kind.contentType)
        sanitizeHeaders(c.headers).forEach { (k, v) -> stream.putHeaders(k, v) }
        streams.addStreams(
            StreamCandidate.newBuilder()
                .setStream(stream)
                .setSourcePage(c.sourcePage)
                .setLadder(Ladder.LADDER_UNSPECIFIED),
        )
    }
    require(streams.streamsCount > 0) { "No streams to cast" }
    return assembleCastRequest(Source.newBuilder().setStreams(streams), target, maxHeight, delivery)
}

// ---------------------------------------------------------------- share intents

enum class ShareChoice { BROWSER, SERVER }

sealed interface ShareAction {
    data class OpenBrowser(val url: String) : ShareAction
    data class PrefillCast(val url: String) : ShareAction
}

fun planShare(url: String, choice: ShareChoice): ShareAction = when (choice) {
    ShareChoice.BROWSER -> ShareAction.OpenBrowser(url)
    ShareChoice.SERVER -> ShareAction.PrefillCast(url)
}

/** Address bar input to a URL to load: http(s) as is, bare hosts get https://, otherwise null. */
fun addressBarUrl(input: String): String? {
    val s = input.trim()
    if (s.isEmpty() || s.any { it.isWhitespace() }) return null
    if (s.contains("://")) return s.takeIf { isHttpUrl(it) }
    return "https://$s".takeIf { isHttpUrl(it) && '.' in s.substringBefore('/') }
}

/** "12s", "3m", "2h": how long ago, for the list. */
fun formatAge(ms: Long): String {
    val s = (ms / 1000).coerceAtLeast(0)
    return when {
        s < 60 -> "${s}s"
        s < 3600 -> "${s / 60}m"
        else -> "${s / 3600}h"
    }
}

/** The site a URL belongs to: its registrable domain, or its host when it has none (an IP address, localhost). */
fun siteOf(url: String): String? {
    val u = url.toHttpUrlOrNull() ?: return null
    return u.topPrivateDomain() ?: u.host
}

/**
 * Whether the page may take the whole browser to [target]. Only the site the user opened may fill the
 * browser: ads stacked over a player navigate the top page away when tapped. Frames are never held to it.
 */
fun mayNavigateTop(site: String?, target: String): Boolean = site == null || siteOf(target) == site
