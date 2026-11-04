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
                prefs.save(base, paired.token, paired.name)
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

    /** Called by the companion's page when the PIN was typed there. */
    @JavascriptInterface
    fun onPaired(token: String) {
        activity.runOnUiThread {
            val uri = activity.webView.url?.let { Uri.parse(it) } ?: return@runOnUiThread
            val host = uri.host
            if (uri.scheme != "http" || host.isNullOrEmpty()) return@runOnUiThread
            prefs.save(Net.baseUrl(host, if (uri.port > 0) uri.port else 80), token, prefs.pcName)
            if (prefs.notifications) {
                activity.ensureNotificationPermission()
                CompanionService.start(activity)
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
        .put("baseUrl", prefs.baseUrl ?: "")
        .put("name", prefs.pcName ?: "")
        .toString()
}
