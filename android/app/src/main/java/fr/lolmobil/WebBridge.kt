package fr.lolmobil

import android.net.Uri
import android.os.Build
import android.webkit.JavascriptInterface
import org.json.JSONObject
import kotlin.concurrent.thread

/**
 * window.LolMobilAndroid in the page. Methods run on a WebView thread;
 * results come back through window callbacks (onDiscovered, onPairResult,
 * onQrResult).
 */
class WebBridge(private val activity: MainActivity) {
    private val prefs = Prefs(activity)

    @JavascriptInterface
    fun discover() {
        thread {
            val list = Discovery.discover(activity)
            activity.js("window.onDiscovered && window.onDiscovered($list)")
        }
    }

    @JavascriptInterface
    fun pair(host: String, port: Int, pin: String) {
        thread {
            val base = Net.baseUrl(host.trim(), if (port > 0) port else 8787)
            try {
                val device = listOfNotNull(Build.MANUFACTURER, Build.MODEL).joinToString(" ").ifEmpty { "Android" }
                val paired = Net.pair(base, pin.trim(), device)
                prefs.saveLan(base, paired.token, paired.name)
                activity.jsCallback("onPairResult", JSONObject().put("ok", true))
                activity.runOnUiThread { activity.openCompanion() }
            } catch (e: Exception) {
                val message = when {
                    e.message.isNullOrEmpty() -> "Le PC ne répond pas"
                    e is java.net.ConnectException || e is java.net.SocketTimeoutException ->
                        "Le PC ne répond pas (même Wi-Fi ? pare-feu Windows ?)"
                    else -> e.message!!
                }
                activity.jsCallback("onPairResult", JSONObject().put("error", message))
            }
        }
    }

    @JavascriptInterface
    fun scanQr() {
        activity.startScan()
    }

    /** Called by the page when the PIN was typed there (Wi-Fi). */
    @JavascriptInterface
    fun onPaired(token: String) {
        activity.runOnUiThread {
            val uri = activity.webView.url?.let { Uri.parse(it) } ?: return@runOnUiThread
            if (uri.host == MainActivity.BUNDLED_HOST) {
                // Bundled interface: same PC, new token.
                val saved = prefs.lanBase ?: return@runOnUiThread
                prefs.saveLan(saved, token, prefs.pcName)
                return@runOnUiThread
            }
            val host = uri.host
            if (uri.scheme != "http" || host.isNullOrEmpty()) return@runOnUiThread
            prefs.saveLan(Net.baseUrl(host, if (uri.port > 0) uri.port else 80), token, prefs.pcName)
            if (prefs.notifications) {
                activity.ensureNotificationPermission()
                CompanionService.start(activity)
            }
        }
    }

    /**
     * The bundled interface cannot reach the PC on the Wi-Fi (some WebViews
     * restrict it): use the page the companion serves instead.
     */
    @JavascriptInterface
    fun useCompanionPage() {
        activity.runOnUiThread {
            val base = prefs.lanBase
            val token = prefs.lanToken
            if (base != null && token != null) {
                activity.webView.loadUrl("$base/#token=$token")
            }
        }
    }

    @JavascriptInterface
    fun retry() {
        activity.runOnUiThread { activity.openStart() }
    }

    @JavascriptInterface
    fun disconnect() {
        activity.disconnect()
    }

    @JavascriptInterface
    fun keepScreenOn(on: Boolean) {
        activity.setKeepScreenOn(on)
    }

    @JavascriptInterface
    fun notificationsEnabled(): Boolean = prefs.notifications

    @JavascriptInterface
    fun setNotifications(on: Boolean) {
        prefs.notifications = on
        activity.runOnUiThread {
            if (on && prefs.isPaired) {
                activity.ensureNotificationPermission()
                CompanionService.start(activity)
            } else {
                CompanionService.stop(activity)
            }
        }
    }

    @JavascriptInterface
    fun requestBatteryExemption() {
        activity.requestBatteryExemption()
    }

    @JavascriptInterface
    fun appVersion(): String = BuildConfig.VERSION_NAME

    @JavascriptInterface
    fun savedPc(): String = JSONObject()
        .put("baseUrl", prefs.lanBase ?: "")
        .put("name", prefs.pcName ?: "")
        .put("remote", prefs.hasRemote)
        .toString()

    /**
     * Wi-Fi details the PC sends through remote access ({urls, token}): the
     * app then connects directly when on the same network.
     */
    @JavascriptInterface
    fun rememberLan(json: String) {
        val data = try {
            JSONObject(json)
        } catch (e: Exception) {
            return
        }
        val token = data.optString("token")
        val list = data.optJSONArray("urls") ?: return
        val urls = (0 until list.length()).map { list.optString(it) }
            .filter { it.startsWith("http://") && it.length < 100 }
        if (token.length < 16 || urls.isEmpty()) return
        activity.runOnUiThread { activity.rememberLan(urls, token) }
    }

    /** Remote access link typed or pasted by hand. */
    @JavascriptInterface
    fun openLink(text: String) {
        val link = Remote.parseLink(text)
        if (link == null) {
            activity.jsCallback("onQrResult", JSONObject().put("error", "Lien d'accès à distance invalide"))
            return
        }
        activity.connectRemote(link)
    }
}
