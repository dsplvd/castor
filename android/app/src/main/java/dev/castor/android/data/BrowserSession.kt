package dev.castor.android.data

import android.annotation.SuppressLint
import android.content.Context
import android.content.MutableContextWrapper
import android.graphics.Bitmap
import android.os.Handler
import android.os.Looper
import android.os.Message
import android.os.SystemClock
import android.util.Log
import android.webkit.CookieManager
import android.webkit.ServiceWorkerClient
import android.webkit.ServiceWorkerController
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import dev.castor.android.domain.CaptureLog
import dev.castor.android.domain.CapturedStream
import dev.castor.android.domain.RawRequest
import dev.castor.android.domain.mayNavigateTop
import dev.castor.android.domain.siteOf
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.json.JSONArray

data class BrowserState(
    val url: String = "",
    val progress: Int = 0,
    val loading: Boolean = false,
    val canGoBack: Boolean = false,
    val canGoForward: Boolean = false,
    val muted: Boolean = false,
    /** The last page outside the opened site that the page tried to take the browser to. */
    val blocked: String? = null,
)

/**
 * A plain user-driven WebView (default user agent, nothing hidden) plus the log of media requests
 * it makes. Requests are only observed: shouldInterceptRequest always returns null, so the
 * WebView loads everything itself. Owned by a ViewModel so it survives rotation; [destroy] on leaving.
 */
class BrowserSession(appContext: Context) {
    val log = CaptureLog()

    private val contextWrapper = MutableContextWrapper(appContext)
    private val app = appContext

    @Volatile private var pageUrl: String = ""
    @Volatile private var destroyed = false
    /** The site the user opened; the page cannot take the browser off it. */
    @Volatile private var site: String? = null
    /** A load the user asked for is under way, so where it redirects becomes the site. */
    @Volatile private var userLoad = false
    /** When the top page last started loading, so a link that went through on its own is not followed twice. */
    private var pageStartedAt = 0L
    private val main = Handler(Looper.getMainLooper())

    private val _state = MutableStateFlow(BrowserState())
    val state: StateFlow<BrowserState> = _state.asStateFlow()

    private val _candidates = MutableStateFlow<List<CapturedStream>>(emptyList())
    val candidates: StateFlow<List<CapturedStream>> = _candidates.asStateFlow()

    val webView: WebView = createWebView()

    private fun observe(request: WebResourceRequest) {
        val url = request.url.toString()
        val cookie = runCatching { CookieManager.getInstance().getCookie(url) }.getOrNull()
        val changed = log.record(
            RawRequest(
                url = url,
                headers = request.requestHeaders.orEmpty(),
                cookie = cookie?.takeIf { it.isNotBlank() },
                pageUrl = pageUrl,
                isMainFrame = request.isForMainFrame,
            ),
        )
        if (changed) _candidates.value = log.snapshot()
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun createWebView(): WebView {
        val view = WebView(contextWrapper)
        view.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            // On, so window.open and target=_blank reach onCreateWindow instead of replacing the page.
            setSupportMultipleWindows(true)
            javaScriptCanOpenWindowsAutomatically = false
            allowFileAccess = false
            allowContentAccess = false
        }
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setAcceptThirdPartyCookies(view, true)
        }
        view.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                runCatching { observe(request) }
                return null
            }

            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val scheme = request.url.scheme?.lowercase()
                if (scheme != "http" && scheme != "https") {
                    Log.i(TAG, "Ignored navigation to $scheme: URL")
                    return true
                }
                if (!request.isForMainFrame) return false
                val target = request.url.toString()
                if (userLoad && request.isRedirect) {
                    site = siteOf(target)
                    return false
                }
                if (mayNavigateTop(site, target)) return false
                block(view, target, request.hasGesture())
                return true
            }

            override fun onPageCommitVisible(view: WebView, url: String) {
                view.evaluateJavascript(RECORD_TAPS_JS, null)
            }

            override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                pageUrl = url
                pageStartedAt = SystemClock.uptimeMillis()
                _state.update { it.copy(url = url, loading = true, progress = 0) }
                sync(view)
            }

            override fun onPageFinished(view: WebView, url: String) {
                pageUrl = url
                userLoad = false
                view.evaluateJavascript(RECORD_TAPS_JS, null)
                _state.update { it.copy(url = url, loading = false, progress = 100) }
                if (_state.value.muted) applyMute(true)
                sync(view)
            }

            override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
                pageUrl = url
                _state.update { it.copy(url = url) }
                sync(view)
            }
        }
        view.webChromeClient = object : WebChromeClient() {
            override fun onProgressChanged(view: WebView, newProgress: Int) {
                _state.update { it.copy(progress = newProgress, loading = newProgress < 100) }
            }

            // No window is ever opened. A tapped one is handed a throwaway view to learn where it was going:
            // the site's own target=_blank links load here, anything else is blocked like a navigation away.
            override fun onCreateWindow(view: WebView, isDialog: Boolean, isUserGesture: Boolean, resultMsg: Message?): Boolean {
                val transport = resultMsg?.obj as? WebView.WebViewTransport
                if (!isUserGesture || transport == null) {
                    Log.i(TAG, "Blocked a popup window")
                    return false
                }
                val probe = WebView(view.context)
                val discard = Runnable { probe.destroy() }
                probe.webViewClient = object : WebViewClient() {
                    override fun shouldOverrideUrlLoading(v: WebView, r: WebResourceRequest): Boolean {
                        main.removeCallbacks(discard)
                        main.post(discard)
                        val target = r.url.toString()
                        if (mayNavigateTop(site, target)) view.loadUrl(target) else block(view, target, gesture = true)
                        return true
                    }
                }
                main.postDelayed(discard, PROBE_LIFETIME_MS)
                transport.webView = probe
                resultMsg.sendToTarget()
                return true
            }
        }
        // Service-worker fetches never reach the page's WebViewClient; observe them the same way.
        ServiceWorkerController.getInstance().setServiceWorkerClient(object : ServiceWorkerClient() {
            override fun shouldInterceptRequest(request: WebResourceRequest): WebResourceResponse? {
                if (!destroyed) runCatching { observe(request) }
                return null
            }
        })
        return view
    }

    /**
     * Keeps the browser off [target]. An ad laid over the page takes the tap meant for the link beneath it,
     * so a tapped one is followed by the link of this site under the finger, unless the page went somewhere on its own.
     */
    private fun block(view: WebView, target: String, gesture: Boolean) {
        Log.i(TAG, "Blocked the page taking the browser to ${target.toHttpUrlOrNull()?.host}")
        _state.update { it.copy(blocked = target) }
        if (!gesture) return
        val blockedAt = SystemClock.uptimeMillis()
        main.postDelayed({
            if (destroyed || pageStartedAt > blockedAt) return@postDelayed
            view.evaluateJavascript(TAPPED_LINKS_JS) { json ->
                val link = tappedLinks(json).firstOrNull { mayNavigateTop(site, it) } ?: return@evaluateJavascript
                Log.i(TAG, "Following the link under the tap instead")
                view.loadUrl(link)
            }
        }, FOLLOW_DELAY_MS)
    }

    private fun tappedLinks(json: String?): List<String> = runCatching {
        val a = JSONArray(json ?: return emptyList())
        List(a.length()) { a.getString(it) }
    }.getOrDefault(emptyList())

    private fun sync(view: WebView) {
        _state.update { it.copy(canGoBack = view.canGoBack(), canGoForward = view.canGoForward()) }
    }

    /** The WebView belongs to [activityContext] while it is on screen. */
    fun attach(activityContext: Context) {
        contextWrapper.baseContext = activityContext
    }

    fun detach() {
        contextWrapper.baseContext = app
    }

    /** Opens [url] as the user's choice: its site becomes the one the page is kept on. */
    fun load(url: String) {
        site = siteOf(url)
        userLoad = true
        _state.update { it.copy(blocked = null) }
        webView.loadUrl(url)
    }

    fun dismissBlocked() = _state.update { it.copy(blocked = null) }
    fun goBack() = webView.goBack()
    fun goForward() = webView.goForward()
    fun reload() = webView.reload()
    fun stopLoading() = webView.stopLoading()

    fun onResume() = webView.onResume()

    /** Leaving the screen: stop whatever the page plays so it is not heard on the phone too. */
    fun onPause(pauseMedia: Boolean) {
        if (pauseMedia) webView.evaluateJavascript(PAUSE_MEDIA_JS, null)
        webView.onPause()
    }

    /** Mutes media in the top page. Players inside cross-origin frames are out of its reach. */
    fun setMuted(muted: Boolean) {
        _state.update { it.copy(muted = muted) }
        applyMute(muted)
    }

    private fun applyMute(muted: Boolean) {
        webView.evaluateJavascript(
            "(function(){window.__castorMute=$muted;" +
                "if(!window.__castorMuteTimer){window.__castorMuteTimer=setInterval(function(){" +
                "document.querySelectorAll('video,audio').forEach(function(m){m.muted=window.__castorMute;});},1000);}" +
                "document.querySelectorAll('video,audio').forEach(function(m){m.muted=$muted;});})()",
            null,
        )
    }

    fun clearCaptured() {
        log.clear()
        _candidates.value = emptyList()
    }

    fun destroy() {
        destroyed = true
        main.removeCallbacksAndMessages(null)
        ServiceWorkerController.getInstance().setServiceWorkerClient(object : ServiceWorkerClient() {})
        webView.stopLoading()
        webView.webViewClient = WebViewClient()
        (webView.parent as? android.view.ViewGroup)?.removeView(webView)
        webView.destroy()
    }

    private companion object {
        const val TAG = "CastorBrowser"
        const val PROBE_LIFETIME_MS = 10_000L
        const val FOLLOW_DELAY_MS = 400L
        const val TAP_MAX_AGE_MS = 3_000

        /** Remembers the links under each tap, beneath whatever sits on top of them. */
        const val RECORD_TAPS_JS =
            "(function(){if(window.__castorTaps)return;window.__castorTaps=1;" +
                "function rec(e){var x=e.clientX,y=e.clientY;if(x==null)return;var hs=[];" +
                "try{document.elementsFromPoint(x,y).forEach(function(el){" +
                "var a=el.closest&&el.closest('a[href]');if(a&&hs.indexOf(a.href)<0)hs.push(a.href);});}catch(_){}" +
                "window.__castorTap={t:Date.now(),hrefs:hs};}" +
                "window.addEventListener('pointerdown',rec,true);window.addEventListener('click',rec,true);})()"

        /** The links under the latest tap, if it was a moment ago. */
        const val TAPPED_LINKS_JS =
            "(function(){var t=window.__castorTap;return t&&Date.now()-t.t<$TAP_MAX_AGE_MS?t.hrefs:null})()"
        const val PAUSE_MEDIA_JS =
            "document.querySelectorAll('video,audio').forEach(function(m){try{m.pause()}catch(e){}})"
    }
}
