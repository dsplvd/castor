package dev.castor.android.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.castor.v1.Device
import com.castor.v1.DeviceType
import dev.castor.android.CastorApp
import dev.castor.android.data.CastorClient
import dev.castor.android.data.Settings
import dev.castor.android.data.TargetKind
import dev.castor.android.data.userMessage
import dev.castor.android.domain.DeliveryPref
import dev.castor.android.domain.StatusLine
import dev.castor.android.domain.TargetChoice
import dev.castor.android.domain.buildCastRequest
import dev.castor.android.domain.describe
import dev.castor.android.domain.isHttpUrl
import dev.castor.android.domain.label
import dev.castor.android.domain.normalizeServerUrl
import dev.castor.android.domain.parseMaxHeight
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

abstract class BaseViewModel(app: Application) : AndroidViewModel(app) {
    protected val castor: CastorApp get() = getApplication()
}

// ---------------------------------------------------------------- settings

data class SettingsUi(
    val serverUrl: String = "",
    val token: String = "",
    val maxHeight: String = "",
    val delivery: DeliveryPref = DeliveryPref.DEFAULT,
    val testing: Boolean = false,
    val testResult: String? = null,
    val testOk: Boolean = false,
    val saved: Boolean = false,
) {
    val maxHeightInvalid: Boolean get() = maxHeight.isNotBlank() && parseMaxHeight(maxHeight) == null
}

class SettingsViewModel(app: Application) : BaseViewModel(app) {
    private val _ui = MutableStateFlow(
        castor.store.settings.value.let {
            SettingsUi(it.serverUrl, it.token, it.maxHeight?.toString().orEmpty(), it.delivery)
        },
    )
    val ui: StateFlow<SettingsUi> = _ui.asStateFlow()

    fun edit(transform: (SettingsUi) -> SettingsUi) =
        _ui.update { transform(it).copy(saved = false, testResult = null) }

    fun save() {
        val u = _ui.value
        if (u.maxHeightInvalid) return
        val url = normalizeServerUrl(u.serverUrl)
        castor.store.update {
            it.copy(
                serverUrl = url ?: u.serverUrl.trim(),
                token = u.token.trim(),
                maxHeight = parseMaxHeight(u.maxHeight),
                delivery = u.delivery,
            )
        }
        _ui.update { it.copy(saved = true, serverUrl = url ?: it.serverUrl) }
    }

    /** Calls ListDevices with the values on screen, saved or not. */
    fun test() {
        val url = normalizeServerUrl(_ui.value.serverUrl)
        if (url == null) {
            _ui.update { it.copy(testResult = "Enter a valid server URL", testOk = false) }
            return
        }
        val client = CastorClient(url, _ui.value.token.trim())
        _ui.update { it.copy(testing = true, testResult = null) }
        viewModelScope.launch {
            val result = runCatching { client.listDevices() }
            _ui.update {
                result.fold(
                    { d -> it.copy(testing = false, testOk = true, testResult = "Connected: ${d.size} device(s) found") },
                    { e -> it.copy(testing = false, testOk = false, testResult = e.userMessage()) },
                )
            }
        }
    }
}

// ---------------------------------------------------------------- devices

data class DevicesUi(
    val devices: List<Device> = emptyList(),
    val loading: Boolean = false,
    val error: String? = null,
    val settings: Settings = Settings(),
)

class DevicesViewModel(app: Application) : BaseViewModel(app) {
    private val _ui = MutableStateFlow(DevicesUi(settings = castor.store.settings.value))
    val ui: StateFlow<DevicesUi> = _ui.asStateFlow()

    init {
        viewModelScope.launch { castor.store.settings.collect { s -> _ui.update { it.copy(settings = s) } } }
        refresh()
    }

    fun refresh() {
        val client = castor.client()
        if (client == null) {
            _ui.update { it.copy(error = "Set the server URL in Settings first") }
            return
        }
        _ui.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            runCatching { client.listDevices() }.fold(
                { d -> _ui.update { it.copy(devices = d, loading = false) } },
                { e -> _ui.update { it.copy(loading = false, error = e.userMessage()) } },
            )
        }
    }

    fun select(device: Device) = castor.store.update {
        it.copy(targetKind = TargetKind.DEVICE, deviceId = device.id, deviceName = device.name)
    }

    fun usePinned(type: DeviceType, address: String) {
        if (address.isBlank()) return
        castor.store.update {
            it.copy(targetKind = TargetKind.PINNED, pinnedType = type, pinnedAddress = address.trim())
        }
    }
}

// ---------------------------------------------------------------- cast

data class RunningCast(val id: String, val device: String, val source: String, val status: StatusLine)

data class CastUi(
    val url: String = "",
    val asPage: Boolean = true,
    val busy: Boolean = false,
    val error: String? = null,
    val target: TargetChoice? = null,
    val running: List<RunningCast> = emptyList(),
    val runningError: String? = null,
)

class CastViewModel(app: Application) : BaseViewModel(app) {
    private val _ui = MutableStateFlow(CastUi(target = castor.store.settings.value.target))
    val ui: StateFlow<CastUi> = _ui.asStateFlow()

    private val _started = Channel<String>(Channel.BUFFERED)

    /** The id of each cast just started, to navigate to Now Playing. */
    val started = _started.receiveAsFlow()

    init {
        viewModelScope.launch { castor.store.settings.collect { s -> _ui.update { it.copy(target = s.target) } } }
    }

    fun setUrl(url: String) = _ui.update { it.copy(url = url, error = null) }
    fun setAsPage(page: Boolean) = _ui.update { it.copy(asPage = page, error = null) }

    /** A page shared from another app. */
    fun prefill(url: String, asPage: Boolean = true) =
        _ui.update { it.copy(url = url, asPage = asPage, error = null) }

    fun cast() {
        val s = castor.store.settings.value
        val client = castor.client()
        val target = s.target
        val url = _ui.value.url.trim()
        val error = when {
            client == null -> "Set the server URL in Settings first"
            target == null -> "Pick a device in Devices first"
            !isHttpUrl(url) -> "Enter an http(s) URL"
            else -> null
        }
        if (error != null || client == null || target == null) {
            _ui.update { it.copy(error = error) }
            return
        }
        val request = buildCastRequest(url, _ui.value.asPage, target, s.maxHeight, s.delivery)
        _ui.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            runCatching { client.cast(request) }.fold(
                { id ->
                    _ui.update { it.copy(busy = false) }
                    _started.send(id)
                },
                { e -> _ui.update { it.copy(busy = false, error = e.userMessage()) } },
            )
        }
    }

    /** Casts the server is playing now, so a running one can be reopened. */
    fun refreshRunning() {
        val client = castor.client() ?: return
        viewModelScope.launch {
            runCatching { client.listCasts() }.fold(
                { casts ->
                    val rows = casts.map { c ->
                        RunningCast(
                            id = c.id,
                            device = when {
                                !c.hasDevice() -> ""
                                c.device.name.isNotBlank() -> c.device.name
                                else -> "${c.device.type.label()} ${c.device.address}"
                            },
                            source = sourceSummary(c),
                            status = describe(c.status),
                        )
                    }
                    _ui.update { it.copy(running = rows, runningError = null) }
                },
                { e -> _ui.update { it.copy(runningError = e.userMessage()) } },
            )
        }
    }

    private fun sourceSummary(c: com.castor.v1.Cast): String = when (c.source.sourceCase) {
        com.castor.v1.Source.SourceCase.STREAM -> c.source.stream.url
        com.castor.v1.Source.SourceCase.PAGES -> c.source.pages.urlsList.firstOrNull().orEmpty()
        else -> ""
    }
}

// ---------------------------------------------------------------- now playing

data class NowPlayingUi(
    val status: StatusLine = StatusLine("Connecting to the server"),
    val connected: Boolean = false,
    val stopping: Boolean = false,
    val error: String? = null,
)

class NowPlayingViewModel(app: Application) : BaseViewModel(app) {
    private val _ui = MutableStateFlow(NowPlayingUi())
    val ui: StateFlow<NowPlayingUi> = _ui.asStateFlow()

    private var castId: String? = null
    private var job: Job? = null

    /** Follows the cast; called whenever the screen comes to the foreground. */
    fun start(id: String) {
        if (castId != id) {
            castId = id
            _ui.value = NowPlayingUi()
        }
        if (_ui.value.status.ended || job?.isActive == true) return
        job = viewModelScope.launch { follow(id) }
    }

    /** Drops the stream when the screen leaves the foreground; start() reconnects. */
    fun pause() {
        job?.cancel()
        job = null
    }

    private suspend fun follow(id: String) {
        val client = castor.client()
        if (client == null) {
            _ui.update { it.copy(error = "Set the server URL in Settings first") }
            return
        }
        var failures = 0
        while (true) {
            try {
                _ui.update { it.copy(error = null) }
                var ended = false
                client.watch(id).collect { update ->
                    failures = 0
                    val line = describe(update)
                    _ui.update { it.copy(status = line, connected = true, error = null) }
                    if (line.ended) ended = true
                }
                if (ended) return
                // The server closes the stream only after Ended.
                throw IllegalStateException("The connection closed before the cast ended")
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _ui.update { it.copy(connected = false, error = e.userMessage()) }
                if (++failures > MAX_RETRIES) return
                delay(RETRY_DELAY_MS)
            }
        }
    }

    /** Retry after giving up (the error banner's button). */
    fun retry() {
        val id = castId ?: return
        pause()
        job = viewModelScope.launch { follow(id) }
    }

    fun stop() {
        val id = castId ?: return
        val client = castor.client() ?: return
        _ui.update { it.copy(stopping = true) }
        viewModelScope.launch {
            runCatching { client.stop(id) }.onFailure { e ->
                _ui.update { it.copy(error = e.userMessage()) }
            }
            _ui.update { it.copy(stopping = false) }
        }
    }

    private companion object {
        const val MAX_RETRIES = 5
        const val RETRY_DELAY_MS = 2_000L
    }
}
