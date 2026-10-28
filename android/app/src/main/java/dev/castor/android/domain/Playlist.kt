package dev.castor.android.domain

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.w3c.dom.Element
import org.xml.sax.InputSource
import java.io.StringReader
import java.util.Locale
import javax.xml.parsers.DocumentBuilderFactory

/** One quality of a stream. [url] is the media playlist for HLS and null for DASH (one manifest holds them all). */
data class Variant(
    val url: String? = null,
    val width: Int? = null,
    val height: Int? = null,
    val bandwidth: Long? = null,
    val averageBandwidth: Long? = null,
    val codecs: String? = null,
    val frameRate: Double? = null,
)

/**
 * What a playlist or manifest says about its stream. [live] is null when it is not known (a master
 * playlist does not say). [drm] names the DRM system (castor cannot play it); [aes] is plain AES-128,
 * which castor can usually play. [error] is set when nothing could be read; the rest is then empty.
 */
data class PlaylistInfo(
    val live: Boolean? = null,
    val variants: List<Variant> = emptyList(),
    val drm: String? = null,
    val aes: Boolean = false,
    val targetDuration: Int? = null,
    val error: String? = null,
) {
    /** The highest quality: tallest, then the highest bitrate. */
    val best: Variant? get() = variants.maxWithOrNull(compareBy<Variant>({ it.height ?: 0 }, { it.averageBandwidth ?: it.bandwidth ?: 0L }))
}

const val MAX_PLAYLIST_BYTES = 2 * 1024 * 1024
const val MAX_VARIANTS = 60

private const val WIDEVINE_UUID = "edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"
private const val PLAYREADY_UUID = "9a04f079-9840-4286-ab92-e65be0885f95"
private const val PLAYREADY_UUID_SWAPPED = "79f0049a-4098-8642-ab92-e65be0885f95"
private const val FAIRPLAY_UUID = "94ce86fb-07ff-4f43-adb8-93d2fa968ca2"
private const val CLEARKEY_UUID = "e2719d58-a985-b3c9-781a-b030af78d30e"

/** Parses [body] by [kind]; HLS and DASH only. Never throws: a bad body is a [PlaylistInfo] with [PlaylistInfo.error]. */
fun parsePlaylist(kind: MediaKind, body: String, url: String): PlaylistInfo = try {
    when (kind) {
        MediaKind.HLS -> parseHls(body, url)
        MediaKind.DASH -> parseDash(body)
        else -> PlaylistInfo(error = "Not a playlist")
    }
} catch (e: Exception) {
    PlaylistInfo(error = "Could not read the playlist")
}

// ---------------------------------------------------------------- HLS

/** Splits an HLS attribute list; quoted values may hold commas (CODECS="avc1.640028,mp4a.40.2"). Names are upper-cased. */
fun parseAttributes(list: String): Map<String, String> {
    val out = LinkedHashMap<String, String>()
    var i = 0
    while (i < list.length) {
        val eq = list.indexOf('=', i)
        if (eq < 0) break
        val name = list.substring(i, eq).trim().uppercase()
        var j = eq + 1
        val value: String
        if (j < list.length && list[j] == '"') {
            val close = list.indexOf('"', j + 1).let { if (it < 0) list.length else it }
            value = list.substring(j + 1, close)
            j = close + 1
        } else {
            val comma = list.indexOf(',', j).let { if (it < 0) list.length else it }
            value = list.substring(j, comma).trim()
            j = comma
        }
        if (name.isNotEmpty()) out[name] = value
        i = list.indexOf(',', j).let { if (it < 0) list.length else it + 1 }
    }
    return out
}

private fun resolveUri(base: String, uri: String): String =
    base.toHttpUrlOrNull()?.resolve(uri)?.toString() ?: uri

/** DRM system named by a key line's attributes, plain-AES flag; both empty for METHOD=NONE. */
private fun keyKind(attrs: Map<String, String>): Pair<String?, Boolean> {
    val method = attrs["METHOD"]?.uppercase() ?: return null to false
    if (method == "NONE") return null to false
    val fmt = attrs["KEYFORMAT"]?.lowercase().orEmpty()
    val name = when {
        WIDEVINE_UUID in fmt || "widevine" in fmt -> "Widevine"
        PLAYREADY_UUID in fmt || "playready" in fmt -> "PlayReady"
        "streamingkeydelivery" in fmt -> "FairPlay"
        else -> null
    }
    return when {
        name != null -> name to false
        method.startsWith("SAMPLE-AES") -> "SAMPLE-AES" to false
        method == "AES-128" -> null to true
        else -> null to false
    }
}

fun parseHls(body: String, baseUrl: String): PlaylistInfo {
    val lines = body.removePrefix("﻿").lineSequence().map { it.trim() }.filter { it.isNotEmpty() }.toList()
    if (lines.firstOrNull()?.startsWith("#EXTM3U") != true) return PlaylistInfo(error = "Not an HLS playlist")
    val variants = ArrayList<Variant>()
    val drm = LinkedHashSet<String>()
    var aes = false
    var endList = false
    var vod = false
    var target: Int? = null
    var master = false
    var i = 0
    while (i < lines.size) {
        val line = lines[i]
        when {
            line.startsWith("#EXT-X-STREAM-INF:") -> {
                master = true
                val a = parseAttributes(line.substringAfter(':'))
                val uri = lines.drop(i + 1).takeWhile { !it.startsWith("#EXT-X-STREAM-INF:") }.firstOrNull { !it.startsWith("#") }
                if (uri != null && variants.size < MAX_VARIANTS) {
                    val res = a["RESOLUTION"]?.lowercase()?.split('x')
                    variants += Variant(
                        url = resolveUri(baseUrl, uri),
                        width = res?.getOrNull(0)?.toIntOrNull(),
                        height = res?.getOrNull(1)?.toIntOrNull(),
                        bandwidth = a["BANDWIDTH"]?.toLongOrNull(),
                        averageBandwidth = a["AVERAGE-BANDWIDTH"]?.toLongOrNull(),
                        codecs = a["CODECS"]?.takeIf { it.isNotBlank() },
                        frameRate = a["FRAME-RATE"]?.toDoubleOrNull(),
                    )
                }
            }
            line.startsWith("#EXT-X-KEY:") || line.startsWith("#EXT-X-SESSION-KEY:") -> {
                val (name, plain) = keyKind(parseAttributes(line.substringAfter(':')))
                name?.let(drm::add)
                if (plain) aes = true
            }
            line.startsWith("#EXT-X-ENDLIST") -> endList = true
            line.startsWith("#EXT-X-PLAYLIST-TYPE:") -> if (line.substringAfter(':').trim().equals("VOD", true)) vod = true
            line.startsWith("#EXT-X-TARGETDURATION:") -> target = line.substringAfter(':').trim().toDoubleOrNull()?.toInt()
        }
        i++
    }
    return PlaylistInfo(
        live = if (master) null else !(endList || vod),
        variants = variants,
        drm = drm.joinToString(", ").ifEmpty { null },
        aes = aes,
        targetDuration = target,
    )
}

// ---------------------------------------------------------------- DASH

private fun Element.name(): String = tagName.substringAfter(':')

private fun Element.children(name: String): List<Element> {
    val out = ArrayList<Element>()
    var n = firstChild
    while (n != null) {
        if (n is Element && n.name() == name) out += n
        n = n.nextSibling
    }
    return out
}

private fun Element.attr(name: String): String? = getAttribute(name).takeIf { it.isNotEmpty() }

private fun parseFrameRate(s: String?): Double? {
    s ?: return null
    val parts = s.split('/')
    val n = parts[0].toDoubleOrNull() ?: return null
    val d = parts.getOrNull(1)?.toDoubleOrNull() ?: 1.0
    return if (d == 0.0) null else n / d
}

fun parseDash(body: String): PlaylistInfo {
    // No DTDs: a manifest has none, and refusing them rules out entity expansion.
    if (body.contains("<!DOCTYPE", ignoreCase = true) || body.contains("<!ENTITY", ignoreCase = true)) {
        return PlaylistInfo(error = "Not a DASH manifest")
    }
    val root = try {
        DocumentBuilderFactory.newInstance().newDocumentBuilder()
            .parse(InputSource(StringReader(body.removePrefix("﻿").trimStart()))).documentElement
    } catch (e: Exception) {
        return PlaylistInfo(error = "Not a DASH manifest")
    }
    if (root.name() != "MPD") return PlaylistInfo(error = "Not a DASH manifest")
    val live = root.getAttribute("type").equals("dynamic", true)

    val sets = ArrayList<Element>()
    root.children("Period").forEach { sets += it.children("AdaptationSet") }
    root.children("AdaptationSet").let { sets += it }

    val variants = ArrayList<Variant>()
    var audioCodec: String? = null
    for (set in sets) {
        for (rep in set.children("Representation")) {
            fun a(n: String) = rep.attr(n) ?: set.attr(n)
            val mime = (a("mimeType") ?: "").lowercase()
            val type = (a("contentType") ?: "").lowercase()
            val width = a("width")?.toIntOrNull()
            val height = a("height")?.toIntOrNull()
            val video = type == "video" || mime.startsWith("video") || width != null || height != null
            if (!video) {
                if (audioCodec == null && (type == "audio" || mime.startsWith("audio"))) audioCodec = a("codecs")
                continue
            }
            if (variants.size >= MAX_VARIANTS) continue
            val v = Variant(
                width = width,
                height = height,
                bandwidth = a("bandwidth")?.toLongOrNull(),
                codecs = a("codecs"),
                frameRate = parseFrameRate(a("frameRate")),
            )
            if (variants.none { it.height == v.height && it.bandwidth == v.bandwidth && it.codecs == v.codecs }) variants += v
        }
    }
    val withAudio = if (audioCodec == null) variants else variants.map { v ->
        v.copy(codecs = listOfNotNull(v.codecs, audioCodec).joinToString(","))
    }

    val named = LinkedHashSet<String>()
    var cenc = false
    val all = root.getElementsByTagName("*")
    for (k in 0 until all.length) {
        val e = all.item(k) as? Element ?: continue
        if (e.name() != "ContentProtection") continue
        val scheme = e.getAttribute("schemeIdUri").lowercase()
        when {
            WIDEVINE_UUID in scheme -> named += "Widevine"
            PLAYREADY_UUID in scheme || PLAYREADY_UUID_SWAPPED in scheme -> named += "PlayReady"
            FAIRPLAY_UUID in scheme -> named += "FairPlay"
            CLEARKEY_UUID in scheme -> named += "ClearKey"
            "mp4protection" in scheme -> cenc = true
            scheme.startsWith("urn:uuid:") -> named += "DRM"
        }
    }
    val drm = named.joinToString(", ").ifEmpty { if (cenc) "encrypted (CENC)" else null }
    return PlaylistInfo(live = live, variants = withAudio, drm = drm)
}

// ---------------------------------------------------------------- formatting

/** "1080p", or null when the height is unknown. */
fun formatResolution(v: Variant): String? = v.height?.let { "${it}p" }

/** "6.5 Mb/s", "800 kb/s". */
fun formatBitrate(bps: Long): String = when {
    bps >= 1_000_000 -> String.format(Locale.ROOT, "%.1f Mb/s", bps / 1_000_000.0)
    else -> "${bps / 1000} kb/s"
}

private val codecNames = mapOf(
    "avc1" to "H.264", "avc3" to "H.264", "hvc1" to "HEVC", "hev1" to "HEVC", "av01" to "AV1",
    "vp09" to "VP9", "vp9" to "VP9", "vp08" to "VP8", "mp4a" to "AAC", "ec-3" to "E-AC-3",
    "ac-3" to "AC-3", "opus" to "Opus",
)

/** "avc1.640028,mp4a.40.2" to "H.264/AAC"; unknown codecs keep their four-character name. */
fun formatCodecs(codecs: String?): String? {
    val names = codecs.orEmpty().split(',').map { it.trim().substringBefore('.').lowercase() }
        .filter { it.isNotEmpty() }.map { codecNames[it] ?: it }.distinct()
    return names.joinToString("/").ifEmpty { null }
}

/** "live" or "VOD"; null when unknown. */
fun formatLive(live: Boolean?): String? = live?.let { if (it) "live" else "VOD" }

/** One variant: "1080p · 6.5 Mb/s · H.264/AAC". */
fun describeVariant(v: Variant): String = listOfNotNull(
    formatResolution(v),
    (v.averageBandwidth ?: v.bandwidth)?.let(::formatBitrate),
    formatCodecs(v.codecs),
).joinToString(" · ")

/** The second line of a candidate: its best variant and live/VOD, "1080p · 6.5 Mb/s · H.264/AAC · live". */
fun describePlaylist(info: PlaylistInfo): String =
    listOfNotNull(info.best?.let(::describeVariant)?.ifEmpty { null }, formatLive(info.live)).joinToString(" · ")
