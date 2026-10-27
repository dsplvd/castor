package dev.castor.android.data

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.castor.v1.DeviceType
import dev.castor.android.domain.DeliveryPref
import dev.castor.android.domain.TargetChoice
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

enum class TargetKind { NONE, DEVICE, PINNED }

data class Settings(
    val serverUrl: String = "",
    val token: String = "",
    val targetKind: TargetKind = TargetKind.NONE,
    val deviceId: String = "",
    val deviceName: String = "",
    val pinnedType: DeviceType = DeviceType.DEVICE_TYPE_DLNA,
    val pinnedAddress: String = "",
    val maxHeight: Int? = null,
    val delivery: DeliveryPref = DeliveryPref.DEFAULT,
) {
    val configured: Boolean get() = serverUrl.isNotBlank()

    /** The target a cast uses, or null if the user has not picked one. */
    val target: TargetChoice?
        get() = when (targetKind) {
            TargetKind.DEVICE -> TargetChoice.Discovered(deviceId, deviceName)
            TargetKind.PINNED -> TargetChoice.Pinned(pinnedType, pinnedAddress)
            TargetKind.NONE -> null
        }
}

/**
 * Settings persistence. Everything lives in EncryptedSharedPreferences (AES-256 values, keys
 * wrapped by an Android Keystore master key), the token included.
 */
class SettingsStore(context: Context) {
    private val prefs: SharedPreferences = open(context.applicationContext)
    private val _settings = MutableStateFlow(read())
    val settings: StateFlow<Settings> = _settings.asStateFlow()

    fun update(transform: (Settings) -> Settings) {
        val next = transform(_settings.value)
        prefs.edit().apply {
            putString("serverUrl", next.serverUrl)
            putString("token", next.token)
            putString("targetKind", next.targetKind.name)
            putString("deviceId", next.deviceId)
            putString("deviceName", next.deviceName)
            putInt("pinnedType", next.pinnedType.number)
            putString("pinnedAddress", next.pinnedAddress)
            putInt("maxHeight", next.maxHeight ?: 0)
            putString("delivery", next.delivery.name)
        }.apply()
        _settings.value = next
    }

    private fun read(): Settings {
        val d = Settings()
        return Settings(
            serverUrl = prefs.getString("serverUrl", d.serverUrl).orEmpty(),
            token = prefs.getString("token", d.token).orEmpty(),
            targetKind = runCatching { TargetKind.valueOf(prefs.getString("targetKind", null).orEmpty()) }
                .getOrDefault(d.targetKind),
            deviceId = prefs.getString("deviceId", "").orEmpty(),
            deviceName = prefs.getString("deviceName", "").orEmpty(),
            pinnedType = DeviceType.forNumber(prefs.getInt("pinnedType", d.pinnedType.number)) ?: d.pinnedType,
            pinnedAddress = prefs.getString("pinnedAddress", "").orEmpty(),
            maxHeight = prefs.getInt("maxHeight", 0).takeIf { it >= 2 },
            delivery = runCatching { DeliveryPref.valueOf(prefs.getString("delivery", null).orEmpty()) }
                .getOrDefault(d.delivery),
        )
    }

    private companion object {
        const val FILE = "castor_settings"

        fun open(context: Context): SharedPreferences {
            fun create(): SharedPreferences {
                val key = MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build()
                return EncryptedSharedPreferences.create(
                    context,
                    FILE,
                    key,
                    EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                    EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
                )
            }
            return try {
                create()
            } catch (e: Exception) {
                // An unreadable store (e.g. a lost Keystore key after a restore) is reset, not fatal.
                context.deleteSharedPreferences(FILE)
                create()
            }
        }
    }
}
