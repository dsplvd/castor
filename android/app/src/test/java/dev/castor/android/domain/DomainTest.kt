package dev.castor.android.domain

import com.castor.v1.CastStatus
import com.castor.v1.CastingStatus
import com.castor.v1.Delivery
import com.castor.v1.DeviceType
import com.castor.v1.Ended
import com.castor.v1.Failure
import com.castor.v1.FailureCode
import com.castor.v1.MeasuringStatus
import com.castor.v1.RecoveryAction
import com.castor.v1.Revision
import com.castor.v1.Source
import com.castor.v1.Target
import com.castor.v1.WatchResponse
import com.google.protobuf.Empty
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DomainTest {
    private val device = TargetChoice.Discovered("dev-1", "Living room")

    @Test
    fun pageBecomesPagesSource() {
        val r = buildCastRequest(" https://example.com/watch?v=1 ", asPage = true, target = device)
        assertEquals(Source.SourceCase.PAGES, r.source.sourceCase)
        assertEquals(listOf("https://example.com/watch?v=1"), r.source.pages.urlsList)
        assertEquals(Target.TargetCase.DEVICE_ID, r.target.targetCase)
        assertEquals("dev-1", r.target.deviceId)
    }

    @Test
    fun directUrlBecomesStreamSourceWithNothingElse() {
        val r = buildCastRequest("http://x/y.m3u8", asPage = false, target = device)
        assertEquals(Source.SourceCase.STREAM, r.source.sourceCase)
        assertEquals("http://x/y.m3u8", r.source.stream.url)
        assertTrue(r.source.stream.headersMap.isEmpty())
        assertEquals("", r.source.stream.contentType)
    }

    @Test
    fun preferencesOmittedWhenUnset() {
        val r = buildCastRequest("https://a.b/c", true, device)
        assertFalse(r.hasPreferences())
    }

    @Test
    fun preferencesCarryOnlyWhatIsSet() {
        val h = buildCastRequest("https://a.b/c", true, device, maxHeight = 1080)
        assertTrue(h.preferences.hasMaxHeight())
        assertEquals(1080, h.preferences.maxHeight)
        assertFalse(h.preferences.hasDelivery())
        assertFalse(h.preferences.hasSubtitles())
        assertFalse(h.preferences.hasAudio())

        val d = buildCastRequest("https://a.b/c", true, device, delivery = DeliveryPref.SERVE)
        assertEquals(Delivery.DELIVERY_SERVE, d.preferences.delivery)
        assertFalse(d.preferences.hasMaxHeight())
    }

    @Test
    fun pinnedTarget() {
        val r = buildCastRequest("https://a.b/c", true, TargetChoice.Pinned(DeviceType.DEVICE_TYPE_ROKU, " 10.0.0.9 "))
        assertEquals(Target.TargetCase.PINNED, r.target.targetCase)
        assertEquals(DeviceType.DEVICE_TYPE_ROKU, r.target.pinned.type)
        assertEquals("10.0.0.9", r.target.pinned.address)
    }

    @Test
    fun sharedTextWithExtraWords() {
        assertEquals(
            "https://example.com/v/1?a=b&c=d",
            extractSharedUrl("Check this out: https://example.com/v/1?a=b&c=d, it is great"),
        )
        assertEquals("https://example.com/x", extractSharedUrl("Title\nhttps://example.com/x."))
        assertEquals("http://a.example/p", extractSharedUrl("(see http://a.example/p) and https://b.example"))
        assertNull(extractSharedUrl("no link here"))
        assertNull(extractSharedUrl(null))
    }

    @Test
    fun serverUrlNormalization() {
        assertEquals("http://192.168.50.32:8411", normalizeServerUrl("192.168.50.32:8411"))
        assertEquals("http://192.168.50.32:8411", normalizeServerUrl(" http://192.168.50.32:8411/ "))
        assertEquals("https://castor.example", normalizeServerUrl("https://castor.example"))
        assertNull(normalizeServerUrl(""))
    }

    @Test
    fun maxHeightValidation() {
        assertEquals(1080, parseMaxHeight("1080"))
        assertNull(parseMaxHeight("1"))
        assertNull(parseMaxHeight(""))
        assertNull(parseMaxHeight("abc"))
    }

    private fun status(b: CastStatus.Builder.() -> Unit) =
        WatchResponse.newBuilder().setStatus(CastStatus.newBuilder().apply(b)).build()

    @Test
    fun statusText() {
        assertEquals("Connecting to the TV", describe(status { setConnecting(Empty.getDefaultInstance()) }).title)
        assertEquals("Finding the video stream", describe(status { setExtracting(Empty.getDefaultInstance()) }).title)

        val measuring = describe(status { setMeasuring(MeasuringStatus.newBuilder().setStreams(3)) })
        assertEquals("Measuring 3 streams", measuring.title)
        assertNull(measuring.detail)
        val measured = describe(status { setMeasuring(MeasuringStatus.newBuilder().setStreams(1).setCastable(0)) })
        assertEquals("Measuring 1 stream", measured.title)
        assertEquals("0 castable", measured.detail)

        val casting = describe(
            status {
                setCasting(
                    CastingStatus.newBuilder().setStreams(4).setCastable(2).setAttempt(2).setRevision(
                        Revision.newBuilder()
                            .setAction(RecoveryAction.RECOVERY_ACTION_SWITCH_CANDIDATE)
                            .setWhy("TV rejected the first stream"),
                    ),
                )
            },
        )
        assertEquals("Casting (attempt 2)", casting.title)
        assertTrue(casting.detail!!.contains("2 of 4 streams castable"))
        assertTrue(casting.detail!!.contains("TV rejected the first stream"))
        assertFalse(casting.ended)
    }

    @Test
    fun endedText() {
        fun ended(b: Ended.Builder.() -> Unit) =
            describe(WatchResponse.newBuilder().setEnded(Ended.newBuilder().apply(b)).build())

        val done = ended { setCompleted(Empty.getDefaultInstance()) }
        assertEquals("Ended: completed", done.title)
        assertTrue(done.ended)
        assertFalse(done.failed)
        assertEquals("Ended: stopped", ended { setStopped(Empty.getDefaultInstance()) }.title)

        val failed = ended {
            setFailed(Failure.newBuilder().setCode(FailureCode.FAILURE_CODE_EXTRACTION_FAILED).setMessage("no video found"))
        }
        assertEquals("Ended: failed", failed.title)
        assertEquals("No stream found: no video found", failed.detail)
        assertTrue(failed.failed && failed.ended)
    }
}
