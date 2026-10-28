package dev.castor.android.domain

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PlaylistTest {
    private val base = "https://cdn.x/v/master.m3u8?t=1"

    @Test
    fun masterPlaylistListsVariantsWithQuotedCodecsAndResolvedUris() {
        val body = """
            #EXTM3U
            #EXT-X-STREAM-INF:BANDWIDTH=6500000,AVERAGE-BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",FRAME-RATE=29.970
            1080/index.m3u8
            #EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720,CODECS="avc1.4d401f,mp4a.40.2"
            https://other.x/720.m3u8
        """.trimIndent()
        val info = parseHls(body, base)
        assertNull(info.error)
        assertNull(info.live)
        assertEquals(2, info.variants.size)
        val v = info.variants[0]
        assertEquals("https://cdn.x/v/1080/index.m3u8", v.url)
        assertEquals(1920, v.width)
        assertEquals(1080, v.height)
        assertEquals(6_500_000L, v.bandwidth)
        assertEquals(6_000_000L, v.averageBandwidth)
        assertEquals("avc1.640028,mp4a.40.2", v.codecs)
        assertEquals(29.97, v.frameRate!!, 0.001)
        assertEquals("https://other.x/720.m3u8", info.variants[1].url)
        assertEquals(1080, info.best?.height)
    }

    @Test
    fun mediaPlaylistIsLiveWithoutEndlistAndVodWithIt() {
        val live = parseHls("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\na.ts\n", base)
        assertEquals(true, live.live)
        assertEquals(6, live.targetDuration)
        val vod = parseHls("#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\na.ts\n#EXT-X-ENDLIST\n", base)
        assertEquals(false, vod.live)
        val typed = parseHls("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:10,\na.ts\n", base)
        assertEquals(false, typed.live)
    }

    @Test
    fun widevineSampleAesKeyIsDrm() {
        val body = """
            #EXTM3U
            #EXT-X-KEY:METHOD=SAMPLE-AES-CTR,URI="data:text/plain;base64,AAAA",KEYFORMAT="urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed",KEYFORMATVERSIONS="1"
            #EXTINF:6,
            a.ts
        """.trimIndent()
        val info = parseHls(body, base)
        assertEquals("Widevine", info.drm)
        assertFalse(info.aes)
    }

    @Test
    fun fairPlayAndPlayReadyKeysAreNamed() {
        assertEquals("FairPlay", parseHls("#EXTM3U\n#EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI=\"skd://x\",KEYFORMAT=\"com.apple.streamingkeydelivery\"\n", base).drm)
        assertEquals("PlayReady", parseHls("#EXTM3U\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"x\",KEYFORMAT=\"urn:uuid:9a04f079-9840-4286-ab92-e65be0885f95\"\n#EXTINF:1,\na.ts", base).drm)
    }

    @Test
    fun aes128WithIdentityKeyIsNotDrm() {
        val info = parseHls("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k.key\",KEYFORMAT=\"identity\"\n#EXTINF:6,\na.ts", base)
        assertNull(info.drm)
        assertTrue(info.aes)
        assertNull(parseHls("#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:6,\na.ts", base).drm)
        assertFalse(parseHls("#EXTM3U\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:6,\na.ts", base).aes)
    }

    @Test
    fun masterWithSessionKeyFlagsDrm() {
        val body = """
            #EXTM3U
            #EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI="skd://k",KEYFORMAT="com.apple.streamingkeydelivery"
            #EXT-X-STREAM-INF:BANDWIDTH=1000000
            v.m3u8
        """.trimIndent()
        val info = parseHls(body, base)
        assertEquals("FairPlay", info.drm)
        assertEquals(1, info.variants.size)
    }

    @Test
    fun dynamicMpdWithWidevineIsLiveDrmWithVideoRepresentations() {
        val mpd = """
            <?xml version="1.0"?>
            <MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="dynamic">
              <Period>
                <AdaptationSet mimeType="video/mp4" frameRate="30000/1001">
                  <ContentProtection schemeIdUri="urn:mpeg:dash:mp4protection:2011" value="cenc"/>
                  <ContentProtection schemeIdUri="urn:uuid:EDEF8BA9-79D6-4ACE-A3C8-27DCD51D21ED"/>
                  <Representation id="a" width="1920" height="1080" bandwidth="5000000" codecs="avc1.640028"/>
                  <Representation id="b" width="1280" height="720" bandwidth="2500000" codecs="avc1.4d401f"/>
                </AdaptationSet>
                <AdaptationSet contentType="audio" mimeType="audio/mp4">
                  <Representation id="c" bandwidth="128000" codecs="mp4a.40.2"/>
                </AdaptationSet>
              </Period>
            </MPD>
        """.trimIndent()
        val info = parseDash(mpd)
        assertNull(info.error)
        assertEquals(true, info.live)
        assertEquals("Widevine", info.drm)
        assertEquals(2, info.variants.size)
        assertEquals("avc1.640028,mp4a.40.2", info.best?.codecs)
        assertEquals(29.97, info.best?.frameRate!!, 0.01)
    }

    @Test
    fun staticMpdWithoutProtectionIsVod() {
        val mpd = """<MPD type="static"><Period><AdaptationSet contentType="video"><Representation width="640" height="360" bandwidth="800000" codecs="vp09.00.10.08"/></AdaptationSet></Period></MPD>"""
        val info = parseDash(mpd)
        assertEquals(false, info.live)
        assertNull(info.drm)
        assertEquals(360, info.variants.single().height)
    }

    @Test
    fun mpdWithOnlyCencSchemeIsEncrypted() {
        val mpd = """<MPD type="static"><Period><AdaptationSet mimeType="video/mp4"><ContentProtection schemeIdUri="urn:mpeg:dash:mp4protection:2011"/><Representation width="640" height="360" bandwidth="1"/></AdaptationSet></Period></MPD>"""
        assertEquals("encrypted (CENC)", parseDash(mpd).drm)
    }

    @Test
    fun formatsResolutionBitrateCodecsAndLive() {
        assertEquals("1080p", formatResolution(Variant(height = 1080)))
        assertNull(formatResolution(Variant()))
        assertEquals("6.5 Mb/s", formatBitrate(6_500_000))
        assertEquals("800 kb/s", formatBitrate(800_000))
        assertEquals("H.264/AAC", formatCodecs("avc1.640028,mp4a.40.2"))
        assertEquals("HEVC/E-AC-3", formatCodecs("hvc1.1.6.L120, ec-3"))
        assertEquals("AV1/AC-3", formatCodecs("av01.0.08M.08,ac-3"))
        assertEquals("VP9", formatCodecs("vp09.00.10.08"))
        assertNull(formatCodecs(null))
        assertEquals("live", formatLive(true))
        assertEquals("VOD", formatLive(false))
        assertNull(formatLive(null))
    }

    @Test
    fun describesTheBestVariantAndLiveState() {
        val info = PlaylistInfo(live = true, variants = listOf(
            Variant(height = 720, bandwidth = 2_000_000),
            Variant(height = 1080, bandwidth = 6_800_000, averageBandwidth = 6_500_000, codecs = "avc1.640028,mp4a.40.2"),
        ))
        assertEquals("1080p · 6.5 Mb/s · H.264/AAC · live", describePlaylist(info))
    }

    @Test
    fun garbageInputIsAnErrorNotAnException() {
        assertNotNull(parsePlaylist(MediaKind.HLS, "<html>nope</html>", base).error)
        assertNotNull(parsePlaylist(MediaKind.HLS, "", base).error)
        assertNotNull(parsePlaylist(MediaKind.DASH, "\u0000\u0001 not xml <<<", base).error)
        assertNotNull(parsePlaylist(MediaKind.DASH, "<!DOCTYPE x [<!ENTITY a 'b'>]><MPD/>", base).error)
        assertNotNull(parsePlaylist(MediaKind.MP4, "x", base).error)
    }
}
