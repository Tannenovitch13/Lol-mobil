package fr.lolmobil

import android.content.Context

/**
 * Saved connection to the PC companion. The Wi-Fi (direct, fast) and the
 * remote access link (from anywhere) are kept side by side: the app uses
 * the Wi-Fi whenever the PC answers there, and remote access otherwise.
 */
class Prefs(context: Context) {
    private val sp = context.applicationContext.getSharedPreferences("lolmobil", Context.MODE_PRIVATE)

    init {
        migrate()
    }

    /** The companion on the Wi-Fi, e.g. http://192.168.1.20:8787. */
    var lanBase: String?
        get() = sp.getString("lanBase", null)
        set(value) = sp.edit().putString("lanBase", value).apply()

    var lanToken: String?
        get() = sp.getString("lanToken", null)
        set(value) = sp.edit().putString("lanToken", value).apply()

    /** Other addresses of the PC on local networks, sent by the PC itself. */
    val lanUrls: List<String>
        get() = sp.getString("lanUrls", null)?.split(",")?.map { it.trim() }?.filter { it.isNotEmpty() }.orEmpty()

    var pcName: String?
        get() = sp.getString("pcName", null)
        set(value) = sp.edit().putString("pcName", value).apply()

    /** Secret of the remote access link. */
    val remoteSecret: String?
        get() = sp.getString("remoteSecret", null)?.takeIf { it.isNotEmpty() }

    /** Your own relay, when the remote access link points to one (else MQTT). */
    val relayUrl: String?
        get() = sp.getString("relayUrl", null)?.takeIf { it.isNotEmpty() }

    /** MQTT brokers from the remote access link (URL-encoded list), if any. */
    val remoteBrokers: String?
        get() = sp.getString("remoteBrokers", null)

    /** Background notifications (match found, your turn). */
    var notifications: Boolean
        get() = sp.getBoolean("notifications", true)
        set(value) = sp.edit().putBoolean("notifications", value).apply()

    val hasRemote: Boolean
        get() = remoteSecret != null

    val hasLan: Boolean
        get() = !lanToken.isNullOrEmpty() && lanCandidates().isNotEmpty()

    val isPaired: Boolean
        get() = hasRemote || hasLan

    /** Wi-Fi addresses to try, the last one that worked first. */
    fun lanCandidates(): List<String> = (listOfNotNull(lanBase?.takeIf { it.isNotEmpty() }) + lanUrls).distinct()

    /** PC on the Wi-Fi (PIN, QR code or the companion's page). */
    fun saveLan(base: String, token: String, name: String?) {
        val e = sp.edit().putString("lanBase", base).putString("lanToken", token)
        if (!name.isNullOrEmpty()) e.putString("pcName", name)
        // Another PC than the one of the remote access link: forget the link.
        val remotePcToken = sp.getString("remoteLanToken", null)
        if (remotePcToken != null && remotePcToken != token) {
            e.remove("remoteSecret").remove("relayUrl").remove("remoteBrokers").remove("remoteLanToken")
        }
        e.apply()
    }

    /** Remote access link: through your relay, or MQTT (relayUrl null). */
    fun saveRemote(relayUrl: String?, secret: String, brokers: String?) {
        val e = sp.edit().putString("remoteSecret", secret).putString("remoteBrokers", brokers)
        if (relayUrl != null) e.putString("relayUrl", relayUrl) else e.remove("relayUrl")
        // Its PC sends its Wi-Fi details on the first connection.
        if (sp.getString("remoteSecret", null) != secret) e.remove("remoteLanToken")
        e.apply()
    }

    /** Wi-Fi details sent by the PC of the remote access link. */
    fun saveLanFromRemote(urls: List<String>, token: String) {
        val e = sp.edit().putString("remoteLanToken", token).putString("lanUrls", urls.joinToString(","))
        if (sp.getString("lanToken", null) != token) {
            // Wi-Fi details of another PC, or none yet: take this PC's.
            e.putString("lanToken", token).remove("lanBase")
        }
        e.apply()
    }

    fun clear() {
        sp.edit().remove("lanBase").remove("lanToken").remove("lanUrls").remove("pcName")
            .remove("remoteSecret").remove("relayUrl").remove("remoteBrokers").remove("remoteLanToken")
            .apply()
    }

    /** Versions before 0.6 kept either the Wi-Fi or the remote link, in "baseUrl"/"token". */
    private fun migrate() {
        if (!sp.contains("baseUrl") && !sp.contains("token")) return
        val base = sp.getString("baseUrl", null)
        val token = sp.getString("token", null)
        val e = sp.edit().remove("baseUrl").remove("token")
        if (!sp.getString("remoteSecret", null).isNullOrEmpty()) {
            if (!base.isNullOrEmpty()) e.putString("relayUrl", base)
        } else if (!base.isNullOrEmpty() && !token.isNullOrEmpty()) {
            e.putString("lanBase", base).putString("lanToken", token)
        }
        e.apply()
    }
}
