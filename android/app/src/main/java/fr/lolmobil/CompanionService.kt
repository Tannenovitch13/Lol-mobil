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
        fun notify(context: Context, id: Int, notification: Notification) {
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
        if (socket == null) connect()
        return START_STICKY
    }

    override fun onDestroy() {
        stopped = true
        handler.removeCallbacksAndMessages(null)
        socket?.close(1000, null)
        socket = null
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

    private fun connect() {
        val base = prefs.baseUrl ?: return
        val token = prefs.token ?: return
        val url = base.replaceFirst("http", "ws") + "/ws?token=" + Uri.encode(token)
        val request = Request.Builder().url(url).build()
        socket = Net.client.newWebSocket(request, Listener())
    }

    private inner class Listener : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            val observe = JSONObject()
                .put("type", "observe")
                .put("paths", JSONArray(listOf(URI_READY, URI_PHASE, URI_SESSION)))
            webSocket.send(observe.toString())
            handler.post {
                if (socket !== webSocket) return@post
                retryMs = 1000L
                connected = true
                updateStatus()
            }
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            handler.post { if (socket === webSocket) handle(text) }
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
        socket = null
        connected = false
        lcuConnected = false
        if (stopped) return
        if (unauthorized) {
            // The PC was reset: the app asks to pair again when opened.
            stopSelf()
            return
        }
        updateStatus()
        handler.postDelayed({ if (!stopped && socket == null) connect() }, retryMs)
        retryMs = (retryMs * 2).coerceAtMost(30_000L)
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
        if (!stopped) notify(this, ID_STATUS, statusNotification())
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
        notify(this, ID_READY, notification)
    }

    private fun showReadyResult(title: String, text: String) {
        if (!readyShown) return
        val notification = NotificationCompat.Builder(this, CHANNEL_MATCH)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setSilent(true)
            .setAutoCancel(true)
            .setTimeoutAfter(15_000)
            .setContentIntent(openAppIntent(this))
            .build()
        notify(this, ID_READY, notification)
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
        notify(this, ID_TURN, notification)
    }
}
