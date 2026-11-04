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

    /** Background notifications (match found, your turn). */
    var notifications: Boolean
        get() = sp.getBoolean("notifications", true)
        set(value) = sp.edit().putBoolean("notifications", value).apply()

    val isPaired: Boolean
        get() = !baseUrl.isNullOrEmpty() && !token.isNullOrEmpty()

    fun save(baseUrl: String, token: String, pcName: String?) {
        sp.edit()
            .putString("baseUrl", baseUrl)
            .putString("token", token)
            .putString("pcName", pcName)
            .apply()
    }

    fun clear() {
        sp.edit().remove("baseUrl").remove("token").remove("pcName").apply()
    }
}
