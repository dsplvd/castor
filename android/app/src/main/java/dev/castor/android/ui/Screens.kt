package dev.castor.android.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.castor.v1.DeviceType
import dev.castor.android.data.TargetKind
import dev.castor.android.domain.DeliveryPref
import dev.castor.android.domain.label
import dev.castor.android.domain.pinnableTypes

// ---------------------------------------------------------------- cast

@Composable
fun CastScreen(
    vm: CastViewModel,
    onOpenCast: (String) -> Unit,
    onNeedSettings: () -> Unit,
    onOpenBrowser: (String) -> Unit = {},
) {
    val ui by vm.ui.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { vm.started.collect { onOpenCast(it) } }
    LifecycleEventEffect(Lifecycle.Event.ON_START) { vm.refreshRunning() }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("Cast", style = MaterialTheme.typography.headlineMedium)
        OutlinedTextField(
            value = ui.url,
            onValueChange = vm::setUrl,
            label = { Text("URL") },
            modifier = Modifier.fillMaxWidth(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            maxLines = 4,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(
                selected = ui.asPage,
                onClick = { vm.setAsPage(true) },
                label = { Text("Page (server extracts the stream)") },
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(
                selected = !ui.asPage,
                onClick = { vm.setAsPage(false) },
                label = { Text("Direct stream URL") },
            )
        }
        Text(
            "To: " + (ui.target?.label() ?: "no device chosen"),
            style = MaterialTheme.typography.bodyMedium,
        )
        ui.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        Button(onClick = vm::cast, enabled = !ui.busy, modifier = Modifier.fillMaxWidth()) {
            if (ui.busy) CircularProgressIndicator(Modifier.height(18.dp), strokeWidth = 2.dp) else Text("Cast")
        }
        OutlinedButton(
            onClick = { onOpenBrowser(ui.url.trim()) },
            modifier = Modifier.fillMaxWidth(),
        ) { Text("Open in browser") }

        if (ui.running.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            Text("Running now", style = MaterialTheme.typography.titleMedium)
            ui.running.forEach { c ->
                Card(Modifier.fillMaxWidth().clickable { onOpenCast(c.id) }) {
                    Column(Modifier.padding(12.dp)) {
                        Text(c.status.title, style = MaterialTheme.typography.titleSmall)
                        if (c.device.isNotBlank()) Text("on ${c.device}")
                        Text(c.source, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }
        ui.runningError?.let {
            TextButton(onClick = onNeedSettings) { Text("Could not list casts: $it") }
        }
    }
}

// ---------------------------------------------------------------- devices

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DevicesScreen(vm: DevicesViewModel) {
    val ui by vm.ui.collectAsStateWithLifecycle()
    val s = ui.settings
    var pinType by rememberSaveable { mutableStateOf(s.pinnedType.number) }
    var pinAddress by rememberSaveable { mutableStateOf(s.pinnedAddress) }

    PullToRefreshBox(isRefreshing = ui.loading, onRefresh = vm::refresh, modifier = Modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize(),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item { Text("Devices", style = MaterialTheme.typography.headlineMedium) }
            ui.error?.let { item { Text(it, color = MaterialTheme.colorScheme.error) } }
            if (ui.devices.isEmpty() && !ui.loading && ui.error == null) {
                item { Text("No devices found. Pull down to refresh, or pin one below.") }
            }
            items(ui.devices, key = { it.id }) { d ->
                val selected = s.targetKind == TargetKind.DEVICE && s.deviceId == d.id
                Row(
                    Modifier.fillMaxWidth().clickable { vm.select(d) },
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RadioButton(selected = selected, onClick = { vm.select(d) })
                    Column {
                        Text(d.name.ifBlank { d.id }, style = MaterialTheme.typography.bodyLarge)
                        Text("${d.type.label()} · ${d.address}", style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
            item {
                Spacer(Modifier.height(16.dp))
                Text("Pinned target", style = MaterialTheme.typography.titleMedium)
                Text(
                    "For a TV the server cannot discover. Address is a host, host:port, or URL.",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            item {
                TypeDropdown(DeviceType.forNumber(pinType) ?: DeviceType.DEVICE_TYPE_DLNA) { pinType = it.number }
            }
            item {
                OutlinedTextField(
                    value = pinAddress,
                    onValueChange = { pinAddress = it },
                    label = { Text("Address") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            item {
                val active = s.targetKind == TargetKind.PINNED
                Button(
                    onClick = { vm.usePinned(DeviceType.forNumber(pinType) ?: DeviceType.DEVICE_TYPE_DLNA, pinAddress) },
                    enabled = pinAddress.isNotBlank(),
                ) { Text(if (active) "Update pinned target" else "Use pinned target") }
                if (active) Text("Pinned target is the default", style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TypeDropdown(selected: DeviceType, onSelect: (DeviceType) -> Unit) {
    var open by remember { mutableStateOf(false) }
    ExposedDropdownMenuBox(expanded = open, onExpandedChange = { open = it }) {
        OutlinedTextField(
            value = selected.label(),
            onValueChange = {},
            readOnly = true,
            label = { Text("Type") },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = open) },
            modifier = Modifier.menuAnchor().fillMaxWidth(),
        )
        ExposedDropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            pinnableTypes.forEach { t ->
                DropdownMenuItem(text = { Text(t.label()) }, onClick = { onSelect(t); open = false })
            }
        }
    }
}

// ---------------------------------------------------------------- settings

@Composable
fun SettingsScreen(vm: SettingsViewModel) {
    val ui by vm.ui.collectAsStateWithLifecycle()
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("Settings", style = MaterialTheme.typography.headlineMedium)
        OutlinedTextField(
            value = ui.serverUrl,
            onValueChange = { v -> vm.edit { it.copy(serverUrl = v) } },
            label = { Text("API server URL") },
            placeholder = { Text("http://192.168.50.32:8411") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = ui.token,
            onValueChange = { v -> vm.edit { it.copy(token = v) } },
            label = { Text("Token") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = ui.maxHeight,
            onValueChange = { v -> vm.edit { it.copy(maxHeight = v.filter(Char::isDigit)) } },
            label = { Text("Max height (optional, e.g. 1080)") },
            isError = ui.maxHeightInvalid,
            supportingText = if (ui.maxHeightInvalid) ({ Text("Must be 2 or more") }) else null,
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
            modifier = Modifier.fillMaxWidth(),
        )
        Text("Delivery", style = MaterialTheme.typography.titleSmall)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf(
                DeliveryPref.DEFAULT to "Server default",
                DeliveryPref.AUTO to "Auto",
                DeliveryPref.SERVE to "Serve",
            ).forEach { (pref, text) ->
                FilterChip(
                    selected = ui.delivery == pref,
                    onClick = { vm.edit { it.copy(delivery = pref) } },
                    label = { Text(text) },
                )
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Button(onClick = vm::save, enabled = !ui.maxHeightInvalid) { Text("Save") }
            OutlinedButton(onClick = vm::test, enabled = !ui.testing) { Text("Test connection") }
            if (ui.testing) CircularProgressIndicator(Modifier.height(20.dp), strokeWidth = 2.dp)
        }
        if (ui.saved) Text("Saved")
        ui.testResult?.let {
            Text(it, color = if (ui.testOk) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error)
        }
    }
}

// ---------------------------------------------------------------- now playing

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NowPlayingScreen(castId: String, vm: NowPlayingViewModel, onBack: () -> Unit) {
    val ui by vm.ui.collectAsStateWithLifecycle()
    // Watch while the screen is in the foreground; coming back reconnects.
    LifecycleEventEffect(Lifecycle.Event.ON_START) { vm.start(castId) }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { vm.pause() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Now playing") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(
                ui.status.title,
                style = MaterialTheme.typography.headlineSmall,
                color = if (ui.status.failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
            )
            ui.status.detail?.let { Text(it, style = MaterialTheme.typography.bodyLarge) }
            if (!ui.status.ended && ui.connected) CircularProgressIndicator()
            ui.error?.let { err ->
                Text(err, color = MaterialTheme.colorScheme.error)
                if (!ui.status.ended) TextButton(onClick = vm::retry) { Text("Reconnect") }
            }
            if (!ui.status.ended) {
                Button(onClick = vm::stop, enabled = !ui.stopping, modifier = Modifier.fillMaxWidth()) {
                    Text("Stop")
                }
            } else {
                OutlinedButton(onClick = onBack, modifier = Modifier.fillMaxWidth()) { Text("Done") }
            }
        }
    }
}
