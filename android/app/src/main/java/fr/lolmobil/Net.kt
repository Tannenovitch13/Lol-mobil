package fr.lolmobil

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.TimeUnit

/** HTTP calls to the PC companion. */
object Net {
    val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(4, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    private val jsonType = "application/json; charset=utf-8".toMediaType()

    fun baseUrl(host: String, port: Int): String {
        val h = if (host.contains(':') && !host.startsWith("[")) "[$host]" else host
        return "http://$h:$port"
    }

    class Paired(val token: String, val name: String)

    /** Trades the PIN shown on the PC for the access token. */
    @Throws(IOException::class)
    fun pair(base: String, pin: String, device: String): Paired {
        val body = JSONObject().put("pin", pin).put("device", device).toString().toRequestBody(jsonType)
        val request = Request.Builder().url("$base/api/pair").post(body).build()
        client.newCall(request).execute().use { response ->
            val text = response.body?.string().orEmpty()
            val json = try {
                JSONObject(text)
            } catch (e: Exception) {
                null
            }
            if (!response.isSuccessful) {
                val message = json?.optString("error").orEmpty()
                throw IOException(if (message.isNotEmpty()) message else "Erreur ${response.code}")
            }
            val token = json?.optString("token").orEmpty()
            if (token.isEmpty()) throw IOException("Réponse invalide du PC")
            return Paired(token, json?.optString("name").orEmpty())
        }
    }

    /** Checks a token and returns the PC name. */
    @Throws(IOException::class)
    fun check(base: String, token: String): String {
        val request = Request.Builder()
            .url("$base/api/check")
            .header("Authorization", "Bearer $token")
            .build()
        client.newCall(request).execute().use { response ->
            if (response.code == 401) throw IOException("Ce téléphone n'est plus autorisé : appaire-le à nouveau")
            if (!response.isSuccessful) throw IOException("Erreur ${response.code}")
            return try {
                JSONObject(response.body?.string().orEmpty()).optString("name")
            } catch (e: Exception) {
                ""
            }
        }
    }

    /** Accepts or declines the current match ("accept" / "decline"). */
    @Throws(IOException::class)
    fun readyCheck(base: String, token: String, action: String) {
        val request = Request.Builder()
            .url("$base/api/ready-check/$action")
            .header("Authorization", "Bearer $token")
            .post(ByteArray(0).toRequestBody(null))
            .build()
        client.newCall(request).execute().use { response ->
            if (!response.isSuccessful) throw IOException("Erreur ${response.code}")
        }
    }
}
