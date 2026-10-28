package dev.castor.android.data

import dev.castor.android.domain.CapturedStream
import dev.castor.android.domain.MAX_PLAYLIST_BYTES
import dev.castor.android.domain.MediaKind
import dev.castor.android.domain.PlaylistInfo
import dev.castor.android.domain.parsePlaylist
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.ByteArrayOutputStream
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

/**
 * Fetches a caught playlist from the phone with exactly the headers the page used and reads what it says.
 * Its cookies only go to the host they were caught for. What was read is cached per URL for the life of
 * the object; a failure is not, so the next look tries again. Nothing is logged: headers hold cookies.
 */
class PlaylistInspector(
    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(8, TimeUnit.SECONDS)
        .callTimeout(15, TimeUnit.SECONDS)
        // Followed by hand, so the page's cookies never go to a host they were not set for.
        .followRedirects(false)
        .followSslRedirects(false)
        .build(),
) {
    private val cache = ConcurrentHashMap<String, PlaylistInfo>()
    private val permits = Semaphore(MAX_CONCURRENT)

    /** Whether [c] is worth inspecting: HLS and DASH only. */
    fun inspectable(c: CapturedStream) = c.kind == MediaKind.HLS || c.kind == MediaKind.DASH

    fun cached(url: String): PlaylistInfo? = cache[url]

    /** Never throws; a failure is a [PlaylistInfo] with an error. */
    suspend fun inspect(c: CapturedStream): PlaylistInfo {
        cache[c.url]?.let { return it }
        val info = permits.withPermit { withContext(Dispatchers.IO) { load(c) } }
        if (info.error == null) cache[c.url] = info
        return info
    }

    private fun load(c: CapturedStream): PlaylistInfo {
        val cookieHost = c.url.toHttpUrlOrNull()?.host
        val first = fetch(c.url, c.headers, c.kind, cookieHost)
        if (c.kind != MediaKind.HLS || first.error != null || first.drm != null || first.variants.isEmpty()) return first
        // A master playlist does not say live or VOD, nor whether segments are keyed: ask its first variant.
        val media = first.variants.firstNotNullOfOrNull { it.url }?.let { fetch(it, c.headers, c.kind, cookieHost) } ?: return first
        if (media.error != null) return first
        return first.copy(
            live = media.live,
            drm = media.drm,
            aes = media.aes,
            targetDuration = media.targetDuration,
        )
    }

    /** Fetches [url] with the caught [headers]; their cookie only goes to [cookieHost], where it was caught. */
    private fun fetch(url: String, headers: Map<String, String>, kind: MediaKind, cookieHost: String?): PlaylistInfo = try {
        follow(url, headers, kind, cookieHost)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        PlaylistInfo(error = if (e is java.io.IOException) "Could not fetch (${e.javaClass.simpleName})" else "Could not inspect")
    }

    private fun follow(url: String, headers: Map<String, String>, kind: MediaKind, cookieHost: String?): PlaylistInfo {
        var target = url.toHttpUrlOrNull() ?: return PlaylistInfo(error = "Not a URL")
        var hops = 0
        while (true) {
            val req = Request.Builder().url(target).apply {
                headers.forEach { (k, v) ->
                    if (!k.equals("Cookie", ignoreCase = true) || target.host == cookieHost) header(k, v)
                }
            }.build()
            val resp = client.newCall(req).execute()
            if (!resp.isRedirect) return read(resp, kind)
            val next = resp.header("Location")?.let { target.resolve(it) }
            resp.close()
            if (next == null || ++hops > MAX_REDIRECTS) return PlaylistInfo(error = "Too many redirects")
            target = next
        }
    }

    private fun read(response: okhttp3.Response, kind: MediaKind): PlaylistInfo =
        response.use { resp ->
            if (!resp.isSuccessful) return PlaylistInfo(error = "HTTP ${resp.code}")
            val body = resp.body ?: return PlaylistInfo(error = "Empty response")
            val out = ByteArrayOutputStream()
            val buf = ByteArray(8192)
            body.byteStream().use { s ->
                while (true) {
                    val n = s.read(buf)
                    if (n < 0) break
                    out.write(buf, 0, n)
                    if (out.size() > MAX_PLAYLIST_BYTES) return PlaylistInfo(error = "Playlist too large")
                }
            }
            parsePlaylist(kind, out.toString(Charsets.UTF_8.name()), resp.request.url.toString())
        }

    private companion object {
        const val MAX_CONCURRENT = 4
        const val MAX_REDIRECTS = 5
    }
}
