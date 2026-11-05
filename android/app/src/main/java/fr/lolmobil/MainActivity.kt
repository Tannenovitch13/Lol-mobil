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
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import androidx.webkit.WebViewAssetLoader
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

        /** The interface bundled in the app, served over https (secure context). */
        const val BUNDLED_HOST = "appassets.androidplatform.net"
        const val BUNDLED_UI = "https://$BUNDLED_HOST/assets/web/index.html"

        /**
         * The same interface over http, for the Wi-Fi: it talks to the PC
         * companion (plain http/ws on the local network) without mixed
         * content. Updating the app updates the interface, not the PC.
         */
        const val BUNDLED_UI_LAN = "http://$BUNDLED_HOST/assets/web/index.html"
    }

    private lateinit var assetLoader: WebViewAssetLoader

    /** What the page talks through: the Wi-Fi, or remote access. */
    private enum class Mode { NONE, LAN, REMOTE }

    private var mode = Mode.NONE
    @Volatile
    private var probing = false

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

        assetLoader = WebViewAssetLoader.Builder()
            .setHttpAllowed(true)
            .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(this))
            .build()
        webView.addJavascriptInterface(WebBridge(this), "LolMobilAndroid")
        webView.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? =
                assetLoader.shouldInterceptRequest(request.url)

            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val url = request.url
                if (url.scheme == "file" || url.host == BUNDLED_HOST) return false
                val known = prefs.lanCandidates() + listOfNotNull(prefs.relayUrl)
                if (known.any { Uri.parse(it).let { b -> b.host == url.host && b.port == url.port } }) return false
                // Anything else (help links...) opens in the browser.
                try {
                    startActivity(Intent(Intent.ACTION_VIEW, url))
                } catch (e: Exception) {
                    // no browser
                }
                return true
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame && (request.url.scheme == "http" || request.url.scheme == "https")) {
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
        // Back home with the app open on remote access: the Wi-Fi is faster.
        if (mode == Mode.REMOTE) switchToLanIfReachable()
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

    /**
     * The Wi-Fi when the PC answers there (direct, much faster), else remote
     * access. With only one of them known, that one.
     */
    fun openCompanion() {
        val token = prefs.lanToken
        val secret = prefs.remoteSecret
        when {
            prefs.hasLan && secret != null -> {
                val candidates = prefs.lanCandidates()
                thread {
                    val base = Net.firstReachable(candidates, token!!)
                    main.post { if (base != null) openLan(base, token) else openRemote(secret) }
                }
            }
            secret != null -> openRemote(secret)
            prefs.hasLan -> openLan(prefs.lanCandidates().first(), token!!)
            else -> {
                showConnect(null)
                return
            }
        }
        if (prefs.notifications) {
            ensureNotificationPermission()
            CompanionService.start(this)
        }
    }

    private fun openLan(base: String, token: String) {
        mode = Mode.LAN
        if (prefs.lanBase != base) prefs.lanBase = base
        // If the Wi-Fi stops answering, the page asks for remote access
        // (retry) when the app has the link (savedPc().remote).
        webView.loadUrl("$BUNDLED_UI_LAN#lan=${Uri.encode(base)}&token=$token")
    }

    private fun openRemote(secret: String) {
        mode = Mode.REMOTE
        val relay = prefs.relayUrl
        if (relay != null) {
            webView.loadUrl("$relay/#k=$secret")
            return
        }
        // From anywhere through MQTT: the interface comes with the app.
        val brokers = prefs.remoteBrokers?.let { "&m=$it" } ?: ""
        webView.loadUrl("$BUNDLED_UI#k=$secret$brokers")
    }

    /** On remote access: use the Wi-Fi instead if the PC answers there. */
    fun switchToLanIfReachable() {
        val token = prefs.lanToken ?: return
        val candidates = prefs.lanCandidates()
        if (candidates.isEmpty() || probing) return
        probing = true
        thread {
            val base = Net.firstReachable(candidates, token)
            probing = false
            if (base != null) {
                main.post { if (mode == Mode.REMOTE) openLan(base, token) }
            }
        }
    }

    /** Wi-Fi details sent by the PC through remote access. */
    fun rememberLan(urls: List<String>, token: String) {
        prefs.saveLanFromRemote(urls, token)
        if (mode == Mode.REMOTE) switchToLanIfReachable()
    }

    fun showConnect(error: String?) {
        mode = Mode.NONE
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

    /**
     * Wi-Fi QR code: http://<pc>:<port>/#token=<token>.
     * Remote access QR code: https://<relay>/#k=<secret>.
     */
    private fun handleScanned(text: String) {
        val link = Remote.parseLink(text)
        if (link != null) {
            connectRemote(link)
            return
        }
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
                prefs.saveLan(base, token, name)
                main.post { openCompanion() }
            } catch (e: Exception) {
                jsCallback("onQrResult", JSONObject().put("error", e.message ?: "Le PC ne répond pas"))
            }
        }
    }

    /**
     * Remote access link: through your relay when the link points to one,
     * else through the public MQTT brokers (nothing to host).
     */
    fun connectRemote(link: Remote.Link) {
        thread {
            try {
                Remote(link.secret)
            } catch (e: Exception) {
                jsCallback("onQrResult", JSONObject().put("error", e.message ?: "Lien invalide"))
                return@thread
            }
            val isRelay = try {
                Net.checkRelay(link.baseUrl)
                true
            } catch (e: Exception) {
                false
            }
            prefs.saveRemote(if (isRelay) link.baseUrl else null, link.secret, link.brokers)
            main.post { openCompanion() }
        }
    }

    fun disconnect() {
        CompanionService.stop(this)
        mode = Mode.NONE
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
