package fr.lolmobil

import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONArray
import org.json.JSONObject
import kotlin.concurrent.thread

/**
 * Stays connected to the PC companion in the background and notifies:
 * match found (with Accept / Decline buttons) and "your turn" in champion
 * select.
 */
class CompanionService : Service() {

    companion object {
        const val CHANNEL_STATUS = "status"
        const val CHANNEL_MATCH = "match"
        const val CHANNEL_TURN = "turn"
        const val ID_STATUS = 1
        const val ID_READY = 2
        const val ID_TURN = 3
        const val ACTION_STOP = "fr.lolmobil.STOP_SERVICE"
        /** Accept/decline through the service's socket (remote access). */
        const val ACTION_READY = "fr.lolmobil.READY_CHECK"
        const val EXTRA_READY_ACTION = "action"

        /** After the Wi-Fi failed, how long remote access is used before trying it again. */
        private const val LAN_RETRY_MS = 60_000L

        private const val URI_READY = "/lol-matchmaking/v1/ready-check"
        private const val URI_PHASE = "/lol-gameflow/v1/gameflow-phase"
        private const val URI_SESSION = "/lol-champ-select/v1/session"

        fun start(context: Context) {
            try {
                ContextCompat.startForegroundService(context, Intent(context, CompanionService::class.java))
            } catch (e: Exception) {
                // Not allowed from the background on recent Android: the
                // app starts it again when it comes to the foreground.
            }
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, CompanionService::class.java))
        }

        fun createChannels(context: Context) {
            if (Build.VERSION.SDK_INT < 26) return
            val nm = context.getSystemService(NotificationManager::class.java) ?: return
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_STATUS, "Connexion au PC", NotificationManager.IMPORTANCE_LOW).apply {
                    description = "Indique que LolMobil reste connecté au PC"
                    setShowBadge(false)
                }
            )
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_MATCH, "Partie trouvée", NotificationManager.IMPORTANCE_HIGH).apply {
                    description = "Accepter ou refuser une partie trouvée"
                    enableVibration(true)
                    vibrationPattern = longArrayOf(0, 400, 200, 400, 200, 400)
                }
            )
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_TURN, "À toi de jouer", NotificationManager.IMPORTANCE_HIGH).apply {
                    description = "Ton tour de bannir ou de choisir un champion"
                    enableVibration(true)
                }
            )
        }

        fun openAppIntent(context: Context): PendingIntent {
            val intent = Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
            return PendingIntent.getActivity(
                context, 0, intent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )
        }

        @SuppressLint("MissingPermission")
        fun postNotification(context: Context, id: Int, notification: Notification) {
            val nm = NotificationManagerCompat.from(context)
            if (!nm.areNotificationsEnabled()) return
            try {
                nm.notify(id, notification)
            } catch (e: SecurityException) {
                // notification permission revoked
            }
        }
    }

    private lateinit var prefs: Prefs
    private val handler = Handler(Looper.getMainLooper())
    private var socket: WebSocket? = null
    private var retryMs = 1000L
    private var stopped = false
    private var connected = false
    private var lcuConnected = false
    private var phase: String? = null
    private var readyShown = false
    private var lastTurnActionId = -1
    private var remote: Remote? = null
    private var nextRequestId = 1

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        prefs = Prefs(this)
        createChannels(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startInForeground()
        if (intent?.action == ACTION_STOP) {
            prefs.notifications = false
            stopSelf()
            return START_NOT_STICKY
        }
        if (!prefs.isPaired) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (socket == null && mqtt == null && !connecting) connect()
        if (intent?.action == ACTION_READY) {
            sendReadyCheck(intent.getStringExtra(EXTRA_READY_ACTION) ?: "accept")
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopped = true
        handler.removeCallbacksAndMessages(null)
        socket?.close(1000, null)
        socket = null
        mqtt?.close()
        mqtt = null
        val nm = NotificationManagerCompat.from(this)
        nm.cancel(ID_READY)
        nm.cancel(ID_TURN)
        super.onDestroy()
    }

    private fun startInForeground() {
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, ID_STATUS, statusNotification(), type)
    }

    // ------------------------------------------------------------ socket

    /** A Wi-Fi probe is running. */
    private var connecting = false

    /** When the Wi-Fi last failed: remote access is used for a while. */
    private var lanFailedAt = 0L

    /** The Wi-Fi when the PC answers there, else remote access. */
    private fun connect() {
        val token = prefs.lanToken
        val candidates = prefs.lanCandidates()
        val secret = prefs.remoteSecret
        val lanWorth = token != null && candidates.isNotEmpty() &&
            System.currentTimeMillis() - lanFailedAt > LAN_RETRY_MS
        if (lanWorth && secret == null) {
            connectLan(candidates.first(), token!!)
            return
        }
        if (lanWorth && secret != null) {
            connecting = true
            thread {
                val base = Net.firstReachable(candidates, token!!)
                handler.post {
                    connecting = false
                    if (stopped || socket != null || mqtt != null) return@post
                    if (base != null) {
                        connectLan(base, token)
                    } else {
                        lanFailedAt = System.currentTimeMillis()
                        connectRemote(secret)
                    }
                }
            }
            return
        }
        if (secret != null) connectRemote(secret) else if (token != null && candidates.isNotEmpty()) {
            connectLan(candidates.first(), token)
        }
    }

    /** Remote access, end-to-end encrypted: through your relay, or the public MQTT brokers. */
    private fun connectRemote(secret: String) {
        val r = try {
            Remote(secret)
        } catch (e: Exception) {
            stopSelf()
            return
        }
        remote = r
        val relay = prefs.relayUrl
        if (relay == null) {
            connectMqtt(r)
            return
        }
        val request = Request.Builder().url(Remote.clientUrl(relay, r.roomId)).build()
        socket = Net.relayClient.newWebSocket(request, Listener())
    }

    private fun connectLan(base: String, token: String) {
        remote = null
        val url = base.replaceFirst("http", "ws") + "/ws?token=" + Uri.encode(token)
        val request = Request.Builder().url(url).build()
        socket = Net.client.newWebSocket(request, Listener())
    }

    private fun observeMessage(): JSONObject = JSONObject()
        .put("type", "observe")
        .put("paths", JSONArray(listOf(URI_READY, URI_PHASE, URI_SESSION)))

    /** Sends a message to the companion (encrypted in remote mode). */
    private fun sendMessage(message: JSONObject): Boolean {
        val text = message.toString()
        val m = mqtt
        if (m != null) {
            val r = remote ?: return false
            val frame = JSONObject().put("t", "msg").put("c", mqttId).put("d", r.seal(text))
            return m.publish(Remote.topicPrefix(r.roomId) + "/host", frame.toString())
        }
        val ws = socket ?: return false
        return ws.send(remote?.seal(text) ?: text)
    }

    // ------------------------------------------------------------ MQTT

    private var mqtt: MqttClient? = null
    private var mqttReady = false
    private var mqttId = ""
    private var brokerIndex = 0
    private var brokerFailures = 0

    private fun brokers(): List<String> {
        val custom = prefs.remoteBrokers?.let { Uri.decode(it) }
            ?.split(",")?.map { it.trim() }?.filter { it.isNotEmpty() }
        return if (custom.isNullOrEmpty()) MqttClient.DEFAULT_BROKERS else custom
    }

    private fun connectMqtt(r: Remote) {
        val list = brokers()
        val url = list[brokerIndex % list.size]
        mqttId = java.util.UUID.randomUUID().toString().replace("-", "").take(12)
        val prefix = Remote.topicPrefix(r.roomId)
        val will = MqttClient.Will(
            "$prefix/host",
            JSONObject().put("t", "close").put("c", mqttId).toString(),
            false
        )
        val client = MqttClient(url, "lmc-$mqttId", will, mqttListener)
        mqtt = client
        client.connect()
    }

    private val mqttListener = object : MqttClient.Listener {
        override fun onConnected(client: MqttClient) {
            if (mqtt !== client) return
            val r = remote ?: return
            brokerFailures = 0
            retryMs = 1000L
            mqttReady = true
            val prefix = Remote.topicPrefix(r.roomId)
            client.subscribe("$prefix/c/$mqttId")
            // Retained: says right away whether the PC is online.
            client.subscribe("$prefix/presence")
            updateStatus()
        }

        override fun onMessage(client: MqttClient, topic: String, text: String) {
            if (mqtt !== client) return
            val r = remote ?: return
            val prefix = Remote.topicPrefix(r.roomId)
            if (topic == "$prefix/presence") {
                connected = text == "1"
                if (connected) {
                    client.publish("$prefix/host", JSONObject().put("t", "open").put("c", mqttId).toString())
                    sendMessage(observeMessage())
                } else {
                    lcuConnected = false
                }
                updateStatus()
                return
            }
            r.open(text)?.let { handle(it) }
        }

        override fun onClosed(client: MqttClient, wasConnected: Boolean) {
            if (mqtt !== client) return
            mqtt = null
            mqttReady = false
            connected = false
            lcuConnected = false
            if (stopped) return
            updateStatus()
            val delay = if (wasConnected) {
                retryMs
            } else {
                // Unreachable broker: try the next one, then back off.
                brokerIndex++
                brokerFailures++
                if (brokerFailures % brokers().size != 0) 300L else retryMs
            }
            if (delay == retryMs) retryMs = (retryMs * 2).coerceAtMost(30_000L)
            handler.postDelayed({ if (!stopped && mqtt == null && socket == null && !connecting) connect() }, delay)
        }
    }

    private val relayPing = object : Runnable {
        override fun run() {
            if (stopped) return
            if (remote != null) socket?.send("ping")
            handler.postDelayed(this, 25_000L)
        }
    }

    private inner class Listener : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            handler.post {
                if (socket !== webSocket) return@post
                retryMs = 1000L
                if (remote == null) {
                    // Wi-Fi: the companion is right there.
                    sendMessage(observeMessage())
                    connected = true
                    updateStatus()
                } else {
                    handler.removeCallbacks(relayPing)
                    handler.postDelayed(relayPing, 25_000L)
                }
            }
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            handler.post { if (socket === webSocket) receive(text) }
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            handler.post { onDisconnected(webSocket, false) }
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            val unauthorized = response?.code == 401
            handler.post { onDisconnected(webSocket, unauthorized) }
        }
    }

    private fun onDisconnected(webSocket: WebSocket, unauthorized: Boolean) {
        if (socket !== webSocket) return
        val wasLan = remote == null
        socket = null
        connected = false
        lcuConnected = false
        if (stopped) return
        if (unauthorized && !prefs.hasRemote) {
            // The PC was reset: the app asks to pair again when opened.
            stopSelf()
            return
        }
        if (wasLan && prefs.hasRemote) {
            // Left the Wi-Fi of the PC: remote access right away.
            lanFailedAt = System.currentTimeMillis()
            updateStatus()
            handler.postDelayed({ if (!stopped && socket == null && mqtt == null && !connecting) connect() }, 300L)
            return
        }
        updateStatus()
        handler.postDelayed({ if (!stopped && socket == null && mqtt == null && !connecting) connect() }, retryMs)
        retryMs = (retryMs * 2).coerceAtMost(30_000L)
    }

    private fun receive(text: String) {
        val r = remote
        if (r == null) {
            handle(text)
            return
        }
        if (text == "pong") return
        if (text.startsWith("{")) {
            // Relay control frame: is the PC connected to the relay?
            val frame = try {
                JSONObject(text)
            } catch (e: Exception) {
                return
            }
            if (frame.optString("t") == "host") {
                connected = frame.optBoolean("online")
                if (connected) sendMessage(observeMessage()) else lcuConnected = false
                updateStatus()
            }
            return
        }
        r.open(text)?.let { handle(it) }
    }

    private fun sendReadyCheck(action: String) {
        val sent = connected && sendMessage(
            JSONObject()
                .put("type", "request")
                .put("id", nextRequestId++)
                .put("method", "POST")
                .put("path", "$URI_READY/$action")
        )
        when {
            !sent -> showReadyResult("Échec", "Pas connecté au PC")
            action == "accept" -> showReadyResult("Partie acceptée ✓", "En attente des autres joueurs…")
            else -> cancelReady()
        }
    }

    private fun handle(text: String) {
        val message = try {
            JSONObject(text)
        } catch (e: Exception) {
            return
        }
        when (message.optString("type")) {
            "hello" -> {
                val name = message.optString("name")
                if (name.isNotEmpty()) prefs.pcName = name
                lcuConnected = message.optJSONObject("status")?.optBoolean("lcu") ?: false
                updateStatus()
            }
            "status" -> {
                lcuConnected = message.optBoolean("lcu")
                updateStatus()
            }
            "event" -> onEvent(message.optString("uri"), message.opt("data"))
        }
    }

    private fun onEvent(uri: String, data: Any?) {
        when (uri) {
            URI_PHASE -> {
                phase = data as? String
                if (phase != "ReadyCheck") cancelReady()
                if (phase != "ChampSelect") {
                    lastTurnActionId = -1
                    NotificationManagerCompat.from(this).cancel(ID_TURN)
                }
                updateStatus()
            }
            URI_READY -> {
                val rc = data as? JSONObject
                val state = rc?.optString("state")
                val response = rc?.optString("playerResponse")
                when {
                    state == "InProgress" && response == "None" -> showReady()
                    state == "InProgress" && response == "Accepted" -> showReadyResult("Partie acceptée ✓", "En attente des autres joueurs…")
                    else -> cancelReady()
                }
            }
            URI_SESSION -> checkTurn(data as? JSONObject)
        }
    }

    private fun checkTurn(session: JSONObject?) {
        if (session == null) return
        val me = session.optInt("localPlayerCellId", -1)
        val groups = session.optJSONArray("actions") ?: return
        for (i in 0 until groups.length()) {
            val group = groups.optJSONArray(i) ?: continue
            for (j in 0 until group.length()) {
                val action = group.optJSONObject(j) ?: continue
                val type = action.optString("type")
                if (action.optInt("actorCellId", -2) != me || type !in setOf("pick", "ban")) continue
                if (!action.optBoolean("isInProgress") || action.optBoolean("completed")) continue
                val id = action.optInt("id")
                if (id != lastTurnActionId) {
                    lastTurnActionId = id
                    showTurn(type)
                }
                return
            }
        }
        NotificationManagerCompat.from(this).cancel(ID_TURN)
    }

    // ------------------------------------------------------------ notifications

    private fun phaseLabel(): String = when (phase) {
        "Lobby" -> "dans le salon"
        "Matchmaking" -> "recherche de partie"
        "ReadyCheck" -> "partie trouvée !"
        "ChampSelect" -> "sélection des champions"
        "GameStart", "InProgress" -> "partie en cours"
        "WaitingForStats", "PreEndOfGame", "EndOfGame" -> "fin de partie"
        else -> "client LoL ouvert"
    }

    private fun statusNotification(): Notification {
        val pc = prefs.pcName?.takeIf { it.isNotEmpty() } ?: "PC"
        val text = when {
            !connected && remote != null && (socket != null || mqttReady) -> "PC hors ligne (à distance)"
            !connected -> "Connexion à $pc…"
            !lcuConnected -> "Connecté · client LoL fermé"
            else -> "Connecté · ${phaseLabel()}"
        }
        val stopIntent = PendingIntent.getService(
            this, 1, Intent(this, CompanionService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, CHANNEL_STATUS)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("LolMobil · $pc")
            .setContentText(text)
            .setOngoing(true)
            .setSilent(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setContentIntent(openAppIntent(this))
            .addAction(0, "Arrêter", stopIntent)
            .build()
    }

    private fun updateStatus() {
        if (!stopped) postNotification(this, ID_STATUS, statusNotification())
    }

    private fun actionIntent(action: String, requestCode: Int): PendingIntent {
        val intent = Intent(this, NotificationActionReceiver::class.java).setAction(action)
        return PendingIntent.getBroadcast(
            this, requestCode, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
    }

    private fun showReady() {
        if (readyShown || MainActivity.visible) return
        readyShown = true
        val notification = NotificationCompat.Builder(this, CHANNEL_MATCH)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Partie trouvée !")
            .setContentText("Accepte avant la fin du compte à rebours")
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setCategory(NotificationCompat.CATEGORY_EVENT)
            .setVibrate(longArrayOf(0, 400, 200, 400, 200, 400))
            .setAutoCancel(true)
            .setTimeoutAfter(20_000)
            .setContentIntent(openAppIntent(this))
            .addAction(0, "Accepter", actionIntent(NotificationActionReceiver.ACCEPT, 10))
            .addAction(0, "Refuser", actionIntent(NotificationActionReceiver.DECLINE, 11))
            .build()
        postNotification(this, ID_READY, notification)
    }

    private fun showReadyResult(title: String, text: String) {
        if (!readyShown && title != "Échec") return
        val notification = NotificationCompat.Builder(this, CHANNEL_MATCH)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setSilent(true)
            .setAutoCancel(true)
            .setTimeoutAfter(15_000)
            .setContentIntent(openAppIntent(this))
            .build()
        postNotification(this, ID_READY, notification)
    }

    private fun cancelReady() {
        readyShown = false
        NotificationManagerCompat.from(this).cancel(ID_READY)
    }

    private fun showTurn(type: String) {
        if (MainActivity.visible) return
        val notification = NotificationCompat.Builder(this, CHANNEL_TURN)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(if (type == "ban") "À toi de bannir !" else "À toi de choisir ton champion !")
            .setContentText("Touche pour ouvrir LolMobil")
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setContentIntent(openAppIntent(this))
            .build()
        postNotification(this, ID_TURN, notification)
    }
}
