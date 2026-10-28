package dev.castor.android.domain

import com.castor.v1.Ladder
import com.castor.v1.Source
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class CaptureTest {
    private val device = TargetChoice.Discovered("dev-1", "TV")

    private fun kind(url: String, accept: String? = null) =
        (classifyUrl(url, accept) as? UrlClass.Candidate)?.kind

    @Test
    fun manifestsAndFilesAreCandidatesQueryIgnored() {
        assertEquals(MediaKind.HLS, kind("https://cdn.x/v/master.m3u8?token=a.mp4"))
        assertEquals(MediaKind.DASH, kind("https://cdn.x/v/manifest.MPD"))
        assertEquals(MediaKind.MP4, kind("https://cdn.x/v/a.mp4?x=1"))
        assertEquals(MediaKind.WEBM, kind("https://cdn.x/v/a.webm"))
        assertNull(kind("https://cdn.x/v/page.html?file=a.mp4"))
    }

    @Test
    fun segmentsAreEvidenceOnly() {
        assertEquals(UrlClass.Evidence, classifyUrl("https://cdn.x/v/seg1.ts?a=1"))
        assertEquals(UrlClass.Evidence, classifyUrl("https://cdn.x/v/seg1.m4s"))
    }

    @Test
    fun acceptHintsDecideOnlyWhenPathIsSilent() {
        assertEquals(MediaKind.HLS, kind("https://x.y/play", "application/vnd.apple.mpegurl, */*"))
        assertEquals(MediaKind.HLS, kind("https://x.y/play", "application/x-mpegURL"))
        assertEquals(MediaKind.DASH, kind("https://x.y/play", "application/dash+xml"))
        assertEquals(MediaKind.VIDEO, kind("https://x.y/stream", "video/*"))
        assertNull(kind("https://x.y/play.js", "video/*"))
        assertNull(kind("https://x.y/play", "text/html"))
    }

    @Test
    fun adsNonHttpAndOversizedAreIgnored() {
        assertEquals(UrlClass.Ignored, classifyUrl("https://pubads.g.doubleclick.net/x.m3u8"))
        assertEquals(UrlClass.Ignored, classifyUrl("https://ads.doubleclick.net/x.mp4"))
        assertEquals(UrlClass.Ignored, classifyUrl("https://cdn.x/vast/x.mp4"))
        assertEquals(UrlClass.Ignored, classifyUrl("https://cdn.x/vpaid.js/x.mp4"))
        assertEquals(UrlClass.Ignored, classifyUrl("https://cdn.x/ads/x.m3u8"))
        assertEquals(UrlClass.Ignored, classifyUrl("blob:https://x.y/1234"))
        assertEquals(UrlClass.Ignored, classifyUrl("data:video/mp4;base64,AAAA"))
        assertEquals(UrlClass.Ignored, classifyUrl("https://cdn.x/" + "a".repeat(9000) + ".m3u8"))
        assertEquals(MediaKind.HLS, kind("https://roads.example/ladder/x.m3u8"))
    }

    @Test
    fun headersDropInvalidAndUnreplayable() {
        val h = sanitizeHeaders(
            mapOf(
                "Referer" to "https://p/", "Range" to "bytes=0-", "Accept-Encoding" to "gzip",
                "Host" to "x", "Connection" to "keep-alive", "Content-Length" to "0",
                "Bad Name" to "v", "X-Ok" to "line\r\nbreak", "X-Good" to " v ", "" to "v",
                "X-Requested-With" to "dev.castor.android",
            ),
        )
        assertEquals(mapOf("Referer" to "https://p/", "X-Good" to "v"), h)
    }

    @Test
    fun cookieMergeFreshWinsAndEmptyIsNotAdded() {
        assertEquals("a=1; b=3; c=4", mergeCookies("a=1; b=2", "b=3; c=4"))
        assertEquals("", sanitizeHeaders(mapOf("A" to "b"), "")["Cookie"].orEmpty())
        assertFalse(sanitizeHeaders(mapOf("A" to "b"), null).containsKey("Cookie"))
        assertEquals("s=1", sanitizeHeaders(mapOf("A" to "b"), "s=1")["Cookie"])
        assertEquals("x=1; s=1", sanitizeHeaders(mapOf("cookie" to "x=1"), "s=1")["Cookie"])
    }

    @Test
    fun headersCapAt64KeepingTheImportantOnes() {
        val raw = (1..100).associate { "X-$it" to "v" } + mapOf("Referer" to "https://p/", "User-Agent" to "ua")
        val h = sanitizeHeaders(raw, "a=1")
        assertEquals(MAX_HEADERS, h.size)
        assertTrue(h.containsKey("Referer") && h.containsKey("User-Agent") && h.containsKey("Cookie"))
    }

    private var now = 1_000L
    private fun req(url: String, accept: String? = null, frame: Boolean = false, page: String = "https://site/p") =
        RawRequest(url, listOfNotNull(accept?.let { "Accept" to it }, "Referer" to page).toMap(), "s=1", page, !frame)

    @Test
    fun logKeepsExactUrlsCountsHitsAndNotesFrames() {
        val log = CaptureLog { now }
        assertTrue(log.record(req("https://cdn/a.m3u8?t=1", frame = true)))
        now += 500
        assertTrue(log.record(req("https://cdn/a.m3u8?t=1")))
        assertTrue(log.record(req("https://cdn/a.m3u8?t=2")))
        assertFalse(log.record(req("https://cdn/seg.ts")))
        assertFalse(log.record(req("blob:https://x/1")))
        val snap = log.snapshot()
        assertEquals(2, snap.size)
        val first = snap.first { it.url.endsWith("t=1") }
        assertEquals(2, first.hits)
        assertTrue(first.fromFrame)
        assertEquals("s=1", first.headers["Cookie"])
        assertEquals("https://site/p", first.sourcePage)
        assertEquals(1, groupCandidates(snap).size)
        assertEquals(1_000L, first.firstSeenMs)
    }

    @Test
    fun rankingPutsManifestsThenActivityThenNewest() {
        val log = CaptureLog { now }
        log.record(req("https://a/v.mp4"))
        now += 10
        log.record(req("https://b/old.m3u8"))
        now += 10
        log.record(req("https://c/new.m3u8"))
        repeat(3) { log.record(req("https://b/seg$it.ts")) }
        val urls = log.snapshot().map { it.url }
        assertEquals(listOf("https://b/old.m3u8", "https://c/new.m3u8", "https://a/v.mp4"), urls)
        assertEquals(setOf("https://b/old.m3u8", "https://c/new.m3u8"), defaultSelection(log.snapshot()))
    }

    @Test
    fun defaultSelectionLeavesOutDrmStreams() {
        val log = CaptureLog()
        log.record(RawRequest("https://a/open.m3u8", emptyMap(), null, "https://p/", true))
        log.record(RawRequest("https://a/locked.m3u8", emptyMap(), null, "https://p/", true))
        assertEquals(setOf("https://a/open.m3u8"), defaultSelection(log.snapshot(), setOf("https://a/locked.m3u8")))
    }

    @Test
    fun defaultSelectionFallsBackToEverythingWithoutManifests() {
        val log = CaptureLog { now }
        log.record(req("https://a/v.mp4"))
        assertEquals(setOf("https://a/v.mp4"), defaultSelection(log.snapshot()))
    }

    private fun cs(url: String, kind: MediaKind, hits: Int = 1, headers: Map<String, String> = emptyMap()) =
        CapturedStream(url, kind, headers, "https://site/p", false, 0, 0, hits)

    @Test
    fun streamsRequestMapsFieldsAndContentTypes() {
        val r = buildStreamsCastRequest(
            listOf(
                cs("https://a/v.mp4", MediaKind.MP4),
                cs("https://a/m.m3u8", MediaKind.HLS, headers = mapOf("Referer" to "https://site/", "Range" to "x")),
                cs("https://a/s", MediaKind.VIDEO),
            ),
            device, maxHeight = 720, delivery = DeliveryPref.SERVE,
        )
        assertEquals(Source.SourceCase.STREAMS, r.source.sourceCase)
        val s = r.source.streams.streamsList
        assertEquals("https://a/m.m3u8", s[0].stream.url)
        assertEquals("application/vnd.apple.mpegurl", s[0].stream.contentType)
        assertEquals(mapOf("Referer" to "https://site/"), s[0].stream.headersMap)
        assertEquals("https://site/p", s[0].sourcePage)
        assertEquals(Ladder.LADDER_UNSPECIFIED, s[0].ladder)
        assertEquals("video/mp4", s[1].stream.contentType)
        assertEquals("", s[2].stream.contentType)
        assertEquals(720, r.preferences.maxHeight)
        assertEquals("dev-1", r.target.deviceId)
    }

    @Test
    fun streamsRequestCapsAtTwentyDedupesAndRejectsEmpty() {
        val many = (1..50).map { cs("https://a/$it.m3u8", MediaKind.HLS, hits = it) }
        val r = buildStreamsCastRequest(many + many.first(), device)
        assertEquals(MAX_SENT_STREAMS, r.source.streams.streamsCount)
        assertEquals("https://a/50.m3u8", r.source.streams.getStreams(0).stream.url)
        try {
            buildStreamsCastRequest(emptyList(), device)
            fail("expected failure")
        } catch (_: IllegalArgumentException) {
        }
    }

    @Test
    fun shareChoiceAndAddressBar() {
        assertEquals(ShareAction.OpenBrowser("https://x/y"), planShare("https://x/y", ShareChoice.BROWSER))
        assertEquals(ShareAction.PrefillCast("https://x/y"), planShare("https://x/y", ShareChoice.SERVER))
        assertEquals("https://example.com/a", addressBarUrl(" example.com/a "))
        assertEquals("http://h/x", addressBarUrl("http://h/x"))
        assertNull(addressBarUrl("javascript:alert(1)"))
        assertNull(addressBarUrl("intent://x"))
        assertNull(addressBarUrl("two words"))
        assertEquals("59s", formatAge(59_999))
        assertEquals("2m", formatAge(125_000))
    }

    @Test
    fun thePageCannotTakeTheBrowserOffTheSiteTheUserOpened() {
        val site = siteOf("https://streamcorner.st/stream/nfl/nfl-network")
        assertEquals("streamcorner.st", site)
        assertTrue(mayNavigateTop(site, "https://www.streamcorner.st/stream/nba"))
        assertTrue(mayNavigateTop(site, "http://cdn.streamcorner.st/x"))
        assertFalse(mayNavigateTop(site, "https://ads.example-casino.com/landing?id=1"))
        assertFalse(mayNavigateTop(site, "https://streamcorner.st.evil.com/"))
        assertEquals("bbc.co.uk", siteOf("https://www.bbc.co.uk/iplayer"))
        assertFalse(mayNavigateTop("bbc.co.uk", "https://other.co.uk/"))
        assertEquals("192.168.50.32", siteOf("http://192.168.50.32:8411/"))
        assertTrue(mayNavigateTop(null, "https://anywhere.example/"))
    }
}
