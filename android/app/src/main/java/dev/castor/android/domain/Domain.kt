package dev.castor.android.domain

import com.castor.v1.CastRequest
import com.castor.v1.CastStatus
import com.castor.v1.Delivery
import com.castor.v1.DeviceType
import com.castor.v1.Ended
import com.castor.v1.FailureCode
import com.castor.v1.Preferences
import com.castor.v1.RecoveryAction
import com.castor.v1.Source
import com.castor.v1.Stream
import com.castor.v1.Target
import com.castor.v1.WatchResponse
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** The delivery the user chose in Settings; DEFAULT leaves the field unset so the server decides. */
enum class DeliveryPref { DEFAULT, AUTO, SERVE }

/** Where a cast plays: a device the server discovered, or one pinned by type and address. */
sealed interface TargetChoice {
    data class Discovered(val id: String, val name: String) : TargetChoice
    data class Pinned(val type: DeviceType, val address: String) : TargetChoice
}

fun TargetChoice.label(): String = when (this) {
    is TargetChoice.Discovered -> name.ifBlank { id }
    is TargetChoice.Pinned -> "${type.label()} $address"
}

fun DeviceType.label(): String = when (this) {
    DeviceType.DEVICE_TYPE_DLNA -> "DLNA"
    DeviceType.DEVICE_TYPE_CHROMECAST -> "Chromecast"
    DeviceType.DEVICE_TYPE_ROKU -> "Roku"
    else -> "Unknown"
}

/** The device families a pinned target may name. */
val pinnableTypes = listOf(DeviceType.DEVICE_TYPE_DLNA, DeviceType.DEVICE_TYPE_CHROMECAST, DeviceType.DEVICE_TYPE_ROKU)

/**
 * Builds the same request as `castor cast player <url>` (page) or `castor cast url <url>` (stream).
 * Preferences are only attached when the user set something; subtitles and audio are never sent,
 * so the server's defaults apply.
 */
fun buildCastRequest(
    url: String,
    asPage: Boolean,
    target: TargetChoice,
    maxHeight: Int? = null,
    delivery: DeliveryPref = DeliveryPref.DEFAULT,
): CastRequest {
    val trimmed = url.trim()
    val source = Source.newBuilder().apply {
        if (asPage) {
            setPages(Source.Pages.newBuilder().addUrls(trimmed))
        } else {
            setStream(Stream.newBuilder().setUrl(trimmed))
        }
    }
    return assembleCastRequest(source, target, maxHeight, delivery)
}

/** Joins a source, the target and the user's Preferences into a request (shared by every source kind). */
fun assembleCastRequest(
    source: Source.Builder,
    target: TargetChoice,
    maxHeight: Int?,
    delivery: DeliveryPref,
): CastRequest {
    val targetMsg = Target.newBuilder().apply {
        when (target) {
            is TargetChoice.Discovered -> setDeviceId(target.id)
            is TargetChoice.Pinned -> setPinned(
                Target.Pinned.newBuilder().setType(target.type).setAddress(target.address.trim()),
            )
        }
    }
    val request = CastRequest.newBuilder().setTarget(targetMsg).setSource(source)
    if (maxHeight != null || delivery != DeliveryPref.DEFAULT) {
        val prefs = Preferences.newBuilder()
        if (maxHeight != null) prefs.setMaxHeight(maxHeight)
        when (delivery) {
            DeliveryPref.AUTO -> prefs.setDelivery(Delivery.DELIVERY_AUTO)
            DeliveryPref.SERVE -> prefs.setDelivery(Delivery.DELIVERY_SERVE)
            DeliveryPref.DEFAULT -> Unit
        }
        request.setPreferences(prefs)
    }
    return request.build()
}

/** Whether [url] is something the server accepts as a source: an http(s) URL. */
fun isHttpUrl(url: String): Boolean {
    val u = url.trim()
    return (u.startsWith("http://") || u.startsWith("https://")) && u.toHttpUrlOrNull() != null
}

/** The first http(s) URL in shared text such as "Watch this https://x.example/a?b=1 now". */
fun extractSharedUrl(text: String?): String? {
    if (text.isNullOrBlank()) return null
    val match = Regex("""https?://[^\s<>"']+""", RegexOption.IGNORE_CASE).find(text) ?: return null
    return match.value.trimEnd('.', ',', ';', ':', '!', '?', ')', ']', '}').takeIf { isHttpUrl(it) }
}

/** Turns what the user typed into a base URL the client can use, or null if it is not one. */
fun normalizeServerUrl(input: String): String? {
    var s = input.trim()
    if (s.isEmpty()) return null
    if (!s.contains("://")) s = "http://$s"
    val url = s.toHttpUrlOrNull() ?: return null
    return url.toString().trimEnd('/')
}

/** A max_height the server accepts (>= 2), or null for blank / invalid. */
fun parseMaxHeight(input: String): Int? = input.trim().toIntOrNull()?.takeIf { it >= 2 }

/** A cast's state in words for people. */
data class StatusLine(
    val title: String,
    val detail: String? = null,
    /** The cast is over: no further updates will come. */
    val ended: Boolean = false,
    val failed: Boolean = false,
)

fun describe(update: WatchResponse): StatusLine = when (update.updateCase) {
    WatchResponse.UpdateCase.STATUS -> describe(update.status)
    WatchResponse.UpdateCase.ENDED -> describe(update.ended)
    else -> StatusLine("Waiting for the server")
}

fun describe(status: CastStatus): StatusLine = when (status.stateCase) {
    CastStatus.StateCase.CONNECTING -> StatusLine("Connecting to the TV")
    CastStatus.StateCase.EXTRACTING -> StatusLine("Finding the video stream")
    CastStatus.StateCase.MEASURING -> {
        val m = status.measuring
        val title = "Measuring ${plural(m.streams, "stream")}"
        val detail = if (m.hasCastable()) "${m.castable} castable" else null
        StatusLine(title, detail)
    }
    CastStatus.StateCase.CASTING -> {
        val c = status.casting
        val detail = buildList {
            add("${c.castable} of ${plural(c.streams, "stream")} castable")
            if (c.hasRevision()) add("${c.revision.action.label()}: ${c.revision.why}")
        }.joinToString("\n")
        StatusLine(if (c.playing) "Playing on the TV" else "Casting (attempt ${c.attempt})", detail)
    }
    else -> StatusLine("Waiting for the server")
}

fun describe(ended: Ended): StatusLine = when (ended.resultCase) {
    Ended.ResultCase.COMPLETED -> StatusLine("Ended: completed", ended = true)
    Ended.ResultCase.STOPPED -> StatusLine("Ended: stopped", ended = true)
    Ended.ResultCase.FAILED -> StatusLine(
        "Ended: failed",
        detail = "${ended.failed.code.label()}: ${ended.failed.message}",
        ended = true,
        failed = true,
    )
    else -> StatusLine("Ended", ended = true)
}

private fun plural(n: Int, noun: String) = if (n == 1) "1 $noun" else "$n ${noun}s"

fun RecoveryAction.label(): String = when (this) {
    RecoveryAction.RECOVERY_ACTION_SWITCH_CANDIDATE -> "Switched stream"
    RecoveryAction.RECOVERY_ACTION_DECODE_AXIS -> "Changed decoding"
    RecoveryAction.RECOVERY_ACTION_RELAX_READ -> "Relaxed reading"
    RecoveryAction.RECOVERY_ACTION_SERVE_INSTEAD -> "Serving instead"
    else -> "Changed plan"
}

fun FailureCode.label(): String = when (this) {
    FailureCode.FAILURE_CODE_PLAYBACK_FAILED -> "Playback failed"
    FailureCode.FAILURE_CODE_DEVICE_UNREACHABLE -> "TV unreachable"
    FailureCode.FAILURE_CODE_EXTRACTION_FAILED -> "No stream found"
    FailureCode.FAILURE_CODE_SERVER_SHUTDOWN -> "Server shut down"
    FailureCode.FAILURE_CODE_INTERNAL -> "Server error"
    else -> "Failed"
}
