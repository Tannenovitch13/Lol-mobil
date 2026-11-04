package fr.lolmobil

import android.content.Context
import android.net.wifi.WifiManager
import org.json.JSONArray
import org.json.JSONObject
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.NetworkInterface
import java.net.SocketTimeoutException

/**
 * Finds LolMobil companions on the Wi-Fi: broadcasts "LOLMOBIL_DISCOVER" on
 * UDP 8788 and collects the answers ({app, name, port, version}).
 */
object Discovery {
    const val PORT = 8788
    private const val MESSAGE = "LOLMOBIL_DISCOVER"

    fun discover(context: Context, timeoutMs: Long = 2500): JSONArray {
        val found = LinkedHashMap<String, JSONObject>()
        val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager?
        val lock = wifi?.createMulticastLock("lolmobil-discovery")
        try {
            lock?.setReferenceCounted(false)
            lock?.acquire()
            DatagramSocket().use { socket ->
                socket.broadcast = true
                socket.soTimeout = 300
                val data = MESSAGE.toByteArray()
                val targets = broadcastAddresses() + InetAddress.getByName("255.255.255.255")
                for (round in 0 until 2) {
                    for (target in targets) {
                        try {
                            socket.send(DatagramPacket(data, data.size, target, PORT))
                        } catch (e: Exception) {
                            // interface without broadcast: ignore
                        }
                    }
                    val end = System.currentTimeMillis() + timeoutMs / 2
                    val buffer = ByteArray(1024)
                    while (System.currentTimeMillis() < end) {
                        val packet = DatagramPacket(buffer, buffer.size)
                        try {
                            socket.receive(packet)
                        } catch (e: SocketTimeoutException) {
                            continue
                        }
                        val text = String(packet.data, 0, packet.length)
                        val json = try {
                            JSONObject(text)
                        } catch (e: Exception) {
                            continue
                        }
                        if (json.optString("app") != "lolmobil") continue
                        val host = packet.address.hostAddress ?: continue
                        val port = json.optInt("port", 8787)
                        found["$host:$port"] = JSONObject()
                            .put("name", json.optString("name", host))
                            .put("host", host)
                            .put("port", port)
                            .put("version", json.optString("version"))
                    }
                }
            }
        } catch (e: Exception) {
            // no network: return what was found
        } finally {
            try {
                lock?.release()
            } catch (e: Exception) {
                // not held
            }
        }
        return JSONArray(found.values.toList())
    }

    private fun broadcastAddresses(): List<InetAddress> {
        val out = ArrayList<InetAddress>()
        try {
            val interfaces = NetworkInterface.getNetworkInterfaces() ?: return out
            for (iface in interfaces.toList()) {
                if (!iface.isUp || iface.isLoopback) continue
                for (address in iface.interfaceAddresses) {
                    address.broadcast?.let { out.add(it) }
                }
            }
        } catch (e: Exception) {
            // ignore
        }
        return out
    }
}
