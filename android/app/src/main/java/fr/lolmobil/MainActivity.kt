package fr.lolmobil

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.view.WindowManager
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import org.json.JSONObject
import kotlin.concurrent.thread

/**
 * Hosts the interface: the local connection screen (assets/connect.html)
 * until a PC is paired, then the web interface served by the PC companion.
 */
class MainActivity : AppCompatActivity() {

    companion object {
        /** True while the app is on screen (no need for notifications then). */
        @Volatile
        var visible = false

        const val CONNECT_PAGE = "file:///android_asset/connect.html"
    }

    lateinit var webView: WebView
        private set
    private lateinit var prefs: Prefs
    private val main = Handler(Looper.getMainLooper())

    private val scanLauncher = registerForActivityResult(ScanContract()) { result ->
        val text = result.contents
        if (text != null) handleScanned(text)
    }

    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)

        webView = WebView(this)
        webView.setBackgroundColor(Color.parseColor("#010a13"))
        setContentView(webView)
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)

        val settings = webView.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.mediaPlaybackRequiresUserGesture = false
        settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW

        webView.addJavascriptInterface(WebBridge(this), "LolMobilAndroid")
        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val url = request.url
                if (url.scheme == "file") return false
                val base = prefs.baseUrl?.let { Uri.parse(it) }
                if (base != null && url.host == base.host && url.port == base.port) return false
                // Anything else (help links...) opens in the browser.
                try {
                    startActivity(Intent(Intent.ACTION_VIEW, url))
                } catch (e: Exception) {
                    // no browser
                }
                return true
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame && request.url.scheme == "http") {
                    showConnect("unreachable")
                }
            }
        }

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (webView.canGoBack()) webView.goBack() else finish()
            }
        })

        openStart()
    }

    override fun onResume() {
        super.onResume()
        visible = true
    }

    override fun onPause() {
        super.onPause()
        visible = false
    }

    override fun onDestroy() {
        webView.destroy()
        super.onDestroy()
    }

    /** Opens the paired PC, or the connection screen. */
    fun openStart() {
        if (prefs.isPaired) openCompanion() else showConnect(null)
    }

    fun openCompanion() {
        val base = prefs.baseUrl
        val token = prefs.token
        if (base == null || token == null) {
            showConnect(null)
            return
        }
        webView.loadUrl("$base/#token=$token")
        if (prefs.notifications) {
            ensureNotificationPermission()
            CompanionService.start(this)
        }
    }

    fun showConnect(error: String?) {
        webView.loadUrl(if (error == null) CONNECT_PAGE else "$CONNECT_PAGE#error=$error")
    }

    /** Runs JavaScript in the page (from any thread). */
    fun js(code: String) {
        main.post { webView.evaluateJavascript(code, null) }
    }

    fun jsCallback(name: String, payload: JSONObject) {
        js("window.$name && window.$name(${payload})")
    }

    fun startScan() {
        main.post {
            val options = ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Scanne le QR code affiché par LolMobil Companion sur le PC")
                .setBeepEnabled(false)
                .setOrientationLocked(false)
            scanLauncher.launch(options)
        }
    }

    /** The QR code holds http://<pc>:<port>/#token=<token>. */
    private fun handleScanned(text: String) {
        val uri = Uri.parse(text.trim())
        val token = Regex("token=([0-9a-fA-F]{16,})").find(uri.fragment ?: text)?.groupValues?.get(1)
        val host = uri.host
        if (uri.scheme != "http" || host.isNullOrEmpty() || token == null) {
            jsCallback("onQrResult", JSONObject().put("error", "Ce QR code ne vient pas de LolMobil Companion"))
            return
        }
        val port = if (uri.port > 0) uri.port else 8787
        connectWithToken(Net.baseUrl(host, port), token)
    }

    fun connectWithToken(base: String, token: String) {
        thread {
            try {
                val name = Net.check(base, token)
                prefs.save(base, token, name)
                main.post { openCompanion() }
            } catch (e: Exception) {
                jsCallback("onQrResult", JSONObject().put("error", e.message ?: "Le PC ne répond pas"))
            }
        }
    }

    fun disconnect() {
        CompanionService.stop(this)
        prefs.clear()
        main.post {
            webView.clearHistory()
            showConnect(null)
        }
    }

    fun ensureNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    fun setKeepScreenOn(on: Boolean) {
        main.post {
            if (on) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            else window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
    }

    /** Asks Android not to suspend the connection when the screen is off. */
    @SuppressLint("BatteryLife")
    fun requestBatteryExemption() {
        main.post {
            val power = getSystemService(POWER_SERVICE) as PowerManager
            if (power.isIgnoringBatteryOptimizations(packageName)) {
                jsCallback("onAndroidNotice", JSONObject().put("text", "Déjà autorisé en arrière-plan ✓"))
                return@post
            }
            try {
                startActivity(
                    Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName"))
                )
            } catch (e: Exception) {
                try {
                    startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                } catch (ignored: Exception) {
                    // not available
                }
            }
        }
    }
}
