package fr.lolmobil

import android.net.Uri
import android.util.Base64
import java.security.MessageDigest
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * End-to-end encryption for remote access through the LolMobil relay. Same
 * derivation as internal/relay/crypto.go and web/js/remote.js:
 * room = hex(SHA-256("lolmobil-room" + secret))[:32],
 * key = SHA-256("lolmobil-key" + secret), AES-256-GCM, wire =
 * base64(nonce || ciphertext || tag).
 */
class Remote(secret: String) {
    /** A remote access link: relay base URL and secret. */
    class Link(val baseUrl: String, val secret: String)

    val roomId: String
    private val key: SecretKeySpec

    init {
        val raw = Base64.decode(secret, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)
        require(raw.size == 32) { "Lien d'accès à distance invalide" }
        roomId = hex(derive("lolmobil-room", raw)).substring(0, 32)
        key = SecretKeySpec(derive("lolmobil-key", raw), "AES")
    }

    fun seal(text: String): String {
        val nonce = ByteArray(12)
        random.nextBytes(nonce)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(128, nonce))
        val sealed = cipher.doFinal(text.toByteArray(Charsets.UTF_8))
        return Base64.encodeToString(nonce + sealed, Base64.NO_WRAP)
    }

    fun open(wire: String): String? = try {
        val data = Base64.decode(wire, Base64.DEFAULT)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, data, 0, 12))
        String(cipher.doFinal(data, 12, data.size - 12), Charsets.UTF_8)
    } catch (e: Exception) {
        null
    }

    companion object {
        private val random = SecureRandom()

        private fun derive(label: String, secret: ByteArray): ByteArray {
            val md = MessageDigest.getInstance("SHA-256")
            md.update(label.toByteArray(Charsets.UTF_8))
            md.update(secret)
            return md.digest()
        }

        private fun hex(bytes: ByteArray): String = bytes.joinToString("") { "%02x".format(it) }

        /** Parses a remote access link: https://<relay>/#k=<secret>. */
        fun parseLink(text: String): Link? {
            val uri = Uri.parse(text.trim())
            val secret = Regex("(?:^|&)k=([A-Za-z0-9_-]{43})").find(uri.fragment ?: "")?.groupValues?.get(1)
                ?: return null
            val host = uri.host ?: return null
            if (uri.scheme != "https" && uri.scheme != "http") return null
            val port = if (uri.port > 0) ":${uri.port}" else ""
            return Link("${uri.scheme}://$host$port", secret)
        }

        /** WebSocket URL of a phone in the room. */
        fun clientUrl(baseUrl: String, roomId: String): String =
            baseUrl.replaceFirst("https://", "wss://").replaceFirst("http://", "ws://") + "/client/" + roomId
    }
}
