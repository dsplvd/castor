package dev.castor.android.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.ArrowForward
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.castor.android.domain.CapturedStream
import dev.castor.android.domain.PlaylistInfo
import dev.castor.android.domain.describePlaylist
import dev.castor.android.domain.describeVariant
import dev.castor.android.domain.formatAge
import dev.castor.android.domain.groupCandidates
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BrowserScreen(
    vm: BrowserViewModel,
    startUrl: String,
    onOpenCast: (String) -> Unit,
    onClose: () -> Unit,
) {
    val ui by vm.ui.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var sheet by remember { mutableStateOf(false) }

    BackHandler(enabled = ui.page.canGoBack) { vm.back() }
    LaunchedEffect(startUrl) { vm.openOnce(startUrl) }
    LaunchedEffect(Unit) { vm.started.collect { sheet = false; onOpenCast(it) } }
    LifecycleEventEffect(Lifecycle.Event.ON_START) { vm.onResume() }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { vm.onLeave() }

    Column(Modifier.fillMaxSize()) {
        AddressBar(ui.page.url, ui.page.loading, ui.page.canGoBack, ui.page.canGoForward, vm, onClose)
        if (ui.page.loading) {
            LinearProgressIndicator(progress = { ui.page.progress / 100f }, modifier = Modifier.fillMaxWidth())
        }
        ui.page.blocked?.let { blocked ->
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    "Stayed on this site; the page tried to open ${blocked.toHttpUrlOrNull()?.host ?: blocked}",
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = vm::openBlocked) { Text("Open") }
                TextButton(onClick = vm::dismissBlocked) { Text("Dismiss") }
            }
        }
        ui.error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 12.dp)) }
        AndroidView(
            factory = {
                vm.attach(context)
                vm.webView.also { (it.parent as? android.view.ViewGroup)?.removeView(it) }
            },
            onRelease = { vm.detach() },
            modifier = Modifier.weight(1f).fillMaxWidth(),
        )
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AssistChip(
                onClick = { sheet = true },
                label = { Text("${ui.candidates.size} streams caught") },
            )
            FilterChip(
                selected = ui.page.muted,
                onClick = { vm.setMuted(!ui.page.muted) },
                label = { Text("Mute page") },
            )
            Button(onClick = { sheet = true }, modifier = Modifier.weight(1f)) { Text("Cast") }
        }
    }

    LaunchedEffect(sheet) { vm.setSheetOpen(sheet) }
    if (sheet) {
        ModalBottomSheet(onDismissRequest = { sheet = false }, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
            CaughtSheet(ui, vm)
        }
    }
    ui.devicePicker?.let { devices ->
        AlertDialog(
            onDismissRequest = vm::dismissPicker,
            title = { Text("Pick a device") },
            text = {
                Column {
                    ui.deviceError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                    if (devices.isEmpty() && ui.deviceError == null) Text("Looking for devices...")
                    devices.forEach { d ->
                        TextButton(onClick = { vm.chooseDevice(d) }, modifier = Modifier.fillMaxWidth()) {
                            Text(d.name.ifBlank { d.id })
                        }
                    }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = vm::dismissPicker) { Text("Cancel") } },
        )
    }
}

@Composable
private fun AddressBar(
    url: String,
    loading: Boolean,
    canBack: Boolean,
    canForward: Boolean,
    vm: BrowserViewModel,
    onClose: () -> Unit,
) {
    var text by remember(url) { mutableStateOf(url) }
    Row(Modifier.fillMaxWidth().padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = onClose) { Icon(Icons.Default.Close, "Close browser") }
        IconButton(onClick = vm::back, enabled = canBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
        IconButton(onClick = vm::forward, enabled = canForward) { Icon(Icons.AutoMirrored.Filled.ArrowForward, "Forward") }
        OutlinedTextField(
            value = text,
            onValueChange = { text = it },
            singleLine = true,
            modifier = Modifier.weight(1f),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
            keyboardActions = KeyboardActions(onGo = { vm.go(text) }),
        )
        IconButton(onClick = vm::reload) {
            Icon(if (loading) Icons.Default.Close else Icons.Default.Refresh, if (loading) "Stop" else "Reload")
        }
    }
}

@Composable
private fun CaughtSheet(ui: BrowserUi, vm: BrowserViewModel) {
    val now = System.currentTimeMillis()
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("${ui.candidates.size} streams caught", style = MaterialTheme.typography.titleLarge)
        if (ui.candidates.isEmpty()) {
            Text("Play the video in the page first. Nothing has been caught yet.")
        } else {
            LazyColumn(Modifier.weight(1f, fill = false)) {
                groupCandidates(ui.candidates).forEach { group ->
                    items(group, key = { it.url }) { c -> CandidateRow(c, c.url in ui.selected, group.size > 1, now, ui.details[c.url], c.url in ui.inspecting) { vm.toggle(c.url) } }
                }
            }
        }
        ui.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (ui.candidates.isNotEmpty()) {
                Button(onClick = { vm.cast(asPage = false) }, enabled = !ui.busy && ui.selected.isNotEmpty()) {
                    Text("Cast ${ui.selected.size} selected")
                }
                TextButton(onClick = vm::clearCaptured) { Text("Clear") }
            }
            OutlinedButton(onClick = { vm.cast(asPage = true) }, enabled = !ui.busy && ui.page.url.isNotBlank()) {
                Text("Let the server extract it")
            }
        }
        Text(
            "Sent to your castor server: each URL with the headers and cookies the page used for it.",
            style = MaterialTheme.typography.bodySmall,
        )
    }
}

@Composable
private fun CandidateRow(
    c: CapturedStream,
    selected: Boolean,
    grouped: Boolean,
    now: Long,
    info: PlaylistInfo?,
    inspecting: Boolean,
    onToggle: () -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }
    Row(Modifier.fillMaxWidth().clickable(onClick = onToggle), verticalAlignment = Alignment.CenterVertically) {
        Checkbox(checked = selected, onCheckedChange = { onToggle() })
        Column(Modifier.weight(1f)) {
            Text("${c.kind.label} · ${c.host}", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyLarge)
            val tail = if (grouped) c.url.substringAfter('?', "").take(24).let { if (it.isEmpty()) c.pathTail else "${c.pathTail}?$it" } else c.pathTail
            val detail = listOf(
                tail,
                if (c.fromFrame) "frame" else "page",
                "${c.hits + c.activity} hits",
                formatAge(now - c.firstSeenMs),
            ).joinToString(" · ")
            Text(detail, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall)
            when {
                info == null -> if (inspecting) Text("inspecting…", style = MaterialTheme.typography.bodySmall)
                info.error != null -> Text(info.error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                else -> {
                    val summary = describePlaylist(info)
                    if (summary.isNotEmpty()) Text(summary, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
                    info.drm?.let {
                        Text("DRM: $it", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.labelMedium)
                    }
                    if (info.aes) Text("AES-128", style = MaterialTheme.typography.bodySmall)
                    if (info.variants.size > 1) {
                        TextButton(onClick = { expanded = !expanded }) {
                            Text("${info.variants.size} qualities" + if (expanded) " ▴" else " ▾")
                        }
                        if (expanded) {
                            info.variants.sortedByDescending { it.height ?: 0 }.forEach { v ->
                                Text(describeVariant(v), style = MaterialTheme.typography.bodySmall)
                            }
                        }
                    }
                }
            }
        }
    }
}
