package dev.castor.android

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.TextButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import dev.castor.android.domain.ShareAction
import dev.castor.android.domain.ShareChoice
import dev.castor.android.domain.extractSharedUrl
import dev.castor.android.domain.planShare
import dev.castor.android.ui.BrowserScreen
import dev.castor.android.ui.BrowserViewModel
import dev.castor.android.ui.CastScreen
import dev.castor.android.ui.CastViewModel
import dev.castor.android.ui.DevicesScreen
import dev.castor.android.ui.DevicesViewModel
import dev.castor.android.ui.NowPlayingScreen
import dev.castor.android.ui.NowPlayingViewModel
import dev.castor.android.ui.SettingsScreen
import dev.castor.android.ui.SettingsViewModel

/** A URL handed to the app by a share (or opt-in VIEW) intent. Fresh instance per delivery. */
class SharedUrl(val url: String, val asPage: Boolean = true)

class MainActivity : ComponentActivity() {
    private var shared by mutableStateOf<SharedUrl?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) shared = urlFrom(intent)
        setContent {
            CastorTheme {
                Surface(Modifier.fillMaxSize()) {
                    CastorApp(shared = shared, onSharedHandled = { shared = null })
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        urlFrom(intent)?.let { shared = it }
    }

    private fun urlFrom(intent: Intent?): SharedUrl? = when (intent?.action) {
        Intent.ACTION_SEND ->
            extractSharedUrl(intent.getStringExtra(Intent.EXTRA_TEXT))?.let { SharedUrl(it) }
        Intent.ACTION_VIEW ->
            extractSharedUrl(intent.dataString)?.let { SharedUrl(it) }
        else -> null
    }
}

@Composable
private fun CastorTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val context = LocalContext.current
    val scheme = when {
        android.os.Build.VERSION.SDK_INT >= 31 ->
            if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        dark -> darkColorScheme()
        else -> lightColorScheme()
    }
    MaterialTheme(colorScheme = scheme, content = content)
}

private data class Tab(val route: String, val label: String, val icon: androidx.compose.ui.graphics.vector.ImageVector)

private val tabs = listOf(
    Tab("cast", "Cast", Icons.Default.Home),
    Tab("devices", "Devices", Icons.Default.Search),
    Tab("settings", "Settings", Icons.Default.Settings),
)

@Composable
private fun CastorApp(shared: SharedUrl?, onSharedHandled: () -> Unit) {
    val nav = rememberNavController()
    val app = LocalContext.current.applicationContext as CastorApp
    val castVm: CastViewModel = viewModel()
    val route = nav.currentBackStackEntryAsState().value?.destination?.route
    val start = if (app.store.settings.value.configured) "cast" else "settings"

    fun goTo(tab: String) = nav.navigate(tab) {
        popUpTo(nav.graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }

    fun apply(action: ShareAction) = when (action) {
        is ShareAction.OpenBrowser -> nav.navigate("browser?url=${android.net.Uri.encode(action.url)}") { launchSingleTop = true }
        is ShareAction.PrefillCast -> {
            castVm.prefill(action.url, true)
            goTo("cast")
        }
    }

    // A shared page can be opened in the in-app browser or cast through the server.
    shared?.let { s ->
        AlertDialog(
            onDismissRequest = onSharedHandled,
            title = { Text("Shared page") },
            text = { Text(s.url, maxLines = 3) },
            confirmButton = {
                TextButton(onClick = { apply(planShare(s.url, ShareChoice.BROWSER)); onSharedHandled() }) {
                    Text("Open in browser")
                }
            },
            dismissButton = {
                TextButton(onClick = { apply(planShare(s.url, ShareChoice.SERVER)); onSharedHandled() }) {
                    Text("Cast page via server")
                }
            },
        )
    }

    Scaffold(
        bottomBar = {
            if (route in tabs.map { it.route }) {
                NavigationBar {
                    tabs.forEach { t ->
                        NavigationBarItem(
                            selected = route == t.route,
                            onClick = { goTo(t.route) },
                            icon = { Icon(t.icon, contentDescription = null) },
                            label = { Text(t.label) },
                        )
                    }
                }
            }
        },
    ) { padding ->
        NavHost(nav, startDestination = start, modifier = Modifier.padding(padding)) {
            composable("cast") {
                CastScreen(
                    vm = castVm,
                    onOpenCast = { id -> nav.navigate("playing/$id") },
                    onNeedSettings = { goTo("settings") },
                    onOpenBrowser = { u -> nav.navigate("browser?url=${android.net.Uri.encode(u)}") { launchSingleTop = true } },
                )
            }
            composable(
                "browser?url={url}",
                arguments = listOf(navArgument("url") { type = NavType.StringType; defaultValue = "" }),
            ) { entry ->
                BrowserScreen(
                    vm = viewModel<BrowserViewModel>(entry),
                    startUrl = entry.arguments?.getString("url").orEmpty(),
                    onOpenCast = { id -> nav.navigate("playing/$id") },
                    onClose = { nav.popBackStack() },
                )
            }
            composable("devices") { DevicesScreen(viewModel<DevicesViewModel>()) }
            composable("settings") { SettingsScreen(viewModel<SettingsViewModel>()) }
            composable(
                "playing/{id}",
                arguments = listOf(navArgument("id") { type = NavType.StringType }),
            ) { entry ->
                val id = entry.arguments?.getString("id").orEmpty()
                NowPlayingScreen(id, viewModel<NowPlayingViewModel>(), onBack = { nav.popBackStack() })
            }
        }
    }
}
