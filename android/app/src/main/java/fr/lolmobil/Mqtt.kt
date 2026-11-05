package fr.lolmobil

import android.os.Handler
import android.os.Looper
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.io.ByteArrayOutputStream

/**
 * Minimal MQTT 3.1.1 client over WebSocket (QoS 0, retained messages, last
 * will, keepalive), for remote access through public brokers. Same protocol
 * as internal/mqtt (Go) and web/js/mqtt.js. Callbacks run on the main thread.
 */
class MqttClient(
    private val url: String,
    private val clientId: String,
    private val will: Will?,
    private val listener: Listener,
) {
    class Will(val topic: String, val payload: String, val retain: Boolean)

    interface Listener {
        fun onConnected(client: MqttClient)
        fun onMessage(client: MqttClient, topic: String, text: String)
        /** Connection lost, or never established (connected = false). */
        fun onClosed(client: MqttClient, wasConnected: Boolean)
    }

    companion object {
        // HiveMQ (Frankfurt) first: closest for players in Europe.
        val DEFAULT_BROKERS = listOf(
            "wss://broker.hivemq.com:8884/mqtt",
            "wss://broker.emqx.io:8084/mqtt",
            "wss://test.mosquitto.org:8081/mqtt",
        )
        private const val KEEPALIVE_S = 30
    }

    private val main = Handler(Looper.getMainLooper())
    private var socket: WebSocket? = null
    private var buffer = ByteArray(0)
    private var packetId = 0
    @Volatile
    private var connected = false
    private var finished = false

    private val ping = object : Runnable {
        override fun run() {
            if (finished) return
            send(byteArrayOf(0xC0.toByte(), 0))
            main.postDelayed(this, KEEPALIVE_S * 2000L / 3)
        }
    }

    private val timeout = Runnable { if (!connected) close() }

    fun connect() {
        val request = Request.Builder().url(url).header("Sec-WebSocket-Protocol", "mqtt").build()
        socket = Net.relayClient.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                webSocket.send(connectPacket().toByteString())
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                feed(bytes.toByteArray())
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                main.post { finish() }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                main.post { finish() }
            }
        })
        main.postDelayed(timeout, 8000)
    }

    private fun finish() {
        if (finished) return
        finished = true
        main.removeCallbacks(ping)
        main.removeCallbacks(timeout)
        listener.onClosed(this, connected)
    }

    fun close() {
        send(byteArrayOf(0xE0.toByte(), 0))
        socket?.close(1000, null)
        main.post { finish() }
    }

    private fun send(data: ByteArray): Boolean = socket?.send(data.toByteString()) ?: false

    // Length-prefixed fields. (Not named writeBytes: ByteArrayOutputStream
    // has a member with that name on recent Android, which would win.)
    private fun ByteArrayOutputStream.putString(text: String) = putField(text.toByteArray(Charsets.UTF_8))

    private fun ByteArrayOutputStream.putField(data: ByteArray) {
        write(data.size shr 8)
        write(data.size and 0xff)
        write(data, 0, data.size)
    }

    private fun packet(header: Int, body: ByteArray): ByteArray {
        val out = ByteArrayOutputStream()
        out.write(header)
        var n = body.size
        do {
            var digit = n % 128
            n /= 128
            if (n > 0) digit = digit or 0x80
            out.write(digit)
        } while (n > 0)
        out.write(body, 0, body.size)
        return out.toByteArray()
    }

    private fun connectPacket(): ByteArray {
        val body = ByteArrayOutputStream()
        body.putString("MQTT")
        body.write(4)
        var flags = 0x02
        if (will != null) flags = flags or 0x04 or (if (will.retain) 0x20 else 0)
        body.write(flags)
        body.write(KEEPALIVE_S shr 8)
        body.write(KEEPALIVE_S and 0xff)
        body.putString(clientId)
        if (will != null) {
            body.putString(will.topic)
            body.putField(will.payload.toByteArray(Charsets.UTF_8))
        }
        return packet(0x10, body.toByteArray())
    }

    fun subscribe(topic: String) {
        packetId = packetId % 65535 + 1
        val body = ByteArrayOutputStream()
        body.write(packetId shr 8)
        body.write(packetId and 0xff)
        body.putString(topic)
        body.write(0)
        send(packet(0x82, body.toByteArray()))
    }

    fun publish(topic: String, text: String, retain: Boolean = false): Boolean {
        val body = ByteArrayOutputStream()
        body.putString(topic)
        val payload = text.toByteArray(Charsets.UTF_8)
        body.write(payload, 0, payload.size)
        return send(packet(0x30 or (if (retain) 1 else 0), body.toByteArray()))
    }

    /** Splits the stream into MQTT packets (on the OkHttp thread). */
    private fun feed(chunk: ByteArray) {
        buffer += chunk
        while (buffer.size >= 2) {
            var length = 0
            var multiplier = 1
            var i = 1
            var complete = false
            while (i < 5 && i < buffer.size) {
                val b = buffer[i].toInt() and 0xff
                length += (b and 0x7f) * multiplier
                if (b and 0x80 == 0) {
                    complete = true
                    break
                }
                multiplier *= 128
                i++
            }
            if (!complete) return
            val start = i + 1
            if (buffer.size < start + length) return
            val header = buffer[0].toInt() and 0xff
            val body = buffer.copyOfRange(start, start + length)
            buffer = buffer.copyOfRange(start + length, buffer.size)
            handlePacket(header shr 4, header and 0x0f, body)
        }
    }

    private fun handlePacket(type: Int, flags: Int, body: ByteArray) {
        when (type) {
            2 -> { // CONNACK
                if (body.size >= 2 && body[1].toInt() == 0) {
                    main.post {
                        connected = true
                        main.removeCallbacks(timeout)
                        main.postDelayed(ping, KEEPALIVE_S * 2000L / 3)
                        listener.onConnected(this)
                    }
                } else {
                    close()
                }
            }
            3 -> { // PUBLISH
                if (body.size < 2) return
                val n = ((body[0].toInt() and 0xff) shl 8) or (body[1].toInt() and 0xff)
                if (body.size < 2 + n) return
                val topic = String(body, 2, n, Charsets.UTF_8)
                var offset = 2 + n
                if ((flags shr 1) and 3 > 0 && body.size >= offset + 2) {
                    send(byteArrayOf(0x40, 2, body[offset], body[offset + 1]))
                    offset += 2
                }
                val text = String(body, offset, body.size - offset, Charsets.UTF_8)
                main.post { listener.onMessage(this, topic, text) }
            }
        }
    }
}
