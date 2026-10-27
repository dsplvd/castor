package dev.castor.android

import android.app.Application
import dev.castor.android.data.CastorClient
import dev.castor.android.data.SettingsStore
import dev.castor.android.domain.normalizeServerUrl

class CastorApp : Application() {
    lateinit var store: SettingsStore
        private set

    private var cached: Triple<String, String, CastorClient>? = null

    override fun onCreate() {
        super.onCreate()
        store = SettingsStore(this)
    }

    /** A client for the saved server, or null until Settings has a valid URL. */
    @Synchronized
    fun client(): CastorClient? {
        val s = store.settings.value
        val url = normalizeServerUrl(s.serverUrl) ?: return null
        cached?.let { (u, t, c) -> if (u == url && t == s.token) return c }
        return CastorClient(url, s.token).also { cached = Triple(url, s.token, it) }
    }
}
