package fr.lolmobil

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import kotlin.concurrent.thread

/** "Accepter" / "Refuser" buttons of the match found notification. */
class NotificationActionReceiver : BroadcastReceiver() {

    companion object {
        const val ACCEPT = "fr.lolmobil.ACCEPT"
        const val DECLINE = "fr.lolmobil.DECLINE"
    }

    override fun onReceive(context: Context, intent: Intent) {
        val action = when (intent.action) {
            ACCEPT -> "accept"
            DECLINE -> "decline"
            else -> return
        }
        val prefs = Prefs(context)
        if (prefs.isRemote) {
            // Through the relay: the service owns the encrypted connection.
            val service = Intent(context, CompanionService::class.java)
                .setAction(CompanionService.ACTION_READY)
                .putExtra(CompanionService.EXTRA_READY_ACTION, action)
            try {
                context.startService(service)
            } catch (e: Exception) {
                result(context, "Échec", "Ouvre LolMobil pour répondre")
            }
            return
        }
        val base = prefs.baseUrl ?: return
        val token = prefs.token ?: return
        val pending = goAsync()
        thread {
            try {
                Net.readyCheck(base, token, action)
                if (action == "accept") {
                    result(context, "Partie acceptée ✓", "En attente des autres joueurs…")
                } else {
                    NotificationManagerCompat.from(context).cancel(CompanionService.ID_READY)
                }
            } catch (e: Exception) {
                result(context, "Échec", e.message ?: "Le PC ne répond pas")
            } finally {
                pending.finish()
            }
        }
    }

    private fun result(context: Context, title: String, text: String) {
        val notification = NotificationCompat.Builder(context, CompanionService.CHANNEL_MATCH)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setSilent(true)
            .setAutoCancel(true)
            .setTimeoutAfter(15_000)
            .setContentIntent(CompanionService.openAppIntent(context))
            .build()
        CompanionService.postNotification(context, CompanionService.ID_READY, notification)
    }
}
