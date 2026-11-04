package fr.lolmobil

import android.content.Context

/** Saved connection to the PC companion. */
class Prefs(context: Context) {
    private val sp = context.applicationContext.getSharedPreferences("lolmobil", Context.MODE_PRIVATE)

    var baseUrl: String?
        get() = sp.getString("baseUrl", null)
        set(value) = sp.edit().putString("baseUrl", value).apply()

    var token: String?
        get() = sp.getString("token", null)
        set(value) = sp.edit().putString("token", value).apply()

    var pcName: String?
        get() = sp.getString("pcName", null)
        set(value) = sp.edit().putString("pcName", value).apply()

    /** Secret of the remote access link when connected through a relay. */
    var remoteSecret: String?
        get() = sp.getString("remoteSecret", null)
        set(value) = sp.edit().putString("remoteSecret", value).apply()

    val isRemote: Boolean
        get() = !remoteSecret.isNullOrEmpty()

    /** Background notifications (match found, your turn). */
    var notifications: Boolean
        get() = sp.getBoolean("notifications", true)
        set(value) = sp.edit().putBoolean("notifications", value).apply()

    val isPaired: Boolean
        get() = !baseUrl.isNullOrEmpty() && (!token.isNullOrEmpty() || isRemote)

    /** PC on the Wi-Fi. */
    fun save(baseUrl: String, token: String, pcName: String?) {
        sp.edit()
            .putString("baseUrl", baseUrl)
            .putString("token", token)
            .putString("pcName", pcName)
            .remove("remoteSecret")
            .apply()
    }

    /** PC reached through a relay (remote access link). */
    fun saveRemote(relayUrl: String, secret: String) {
        sp.edit()
            .putString("baseUrl", relayUrl)
            .putString("remoteSecret", secret)
            .remove("token")
            .apply()
    }

    fun clear() {
        sp.edit().remove("baseUrl").remove("token").remove("pcName").remove("remoteSecret").apply()
    }
}
