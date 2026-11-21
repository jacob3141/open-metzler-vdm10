package io.github.jacob3141.doorbell

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.RingtoneManager
import androidx.core.app.NotificationCompat
import java.text.DateFormat
import java.util.Date

object Notifications {
    const val CH_STATUS = "status"
    const val CH_RING = "ring"
    const val CH_INFO = "info"

    const val ID_STATUS = 1
    const val ID_RING = 2
    private const val ID_MISSED = 3
    private const val ID_MESSAGE = 4

    fun createChannels(c: Context) {
        val nm = c.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CH_STATUS, c.getString(R.string.ch_status), NotificationManager.IMPORTANCE_MIN).apply {
                description = c.getString(R.string.ch_status_desc)
                setShowBadge(false)
            },
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_RING, c.getString(R.string.ch_ring), NotificationManager.IMPORTANCE_HIGH).apply {
                description = c.getString(R.string.ch_ring_desc)
                setSound(
                    RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE),
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_NOTIFICATION_RINGTONE)
                        .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                        .build(),
                )
                enableVibration(true)
                vibrationPattern = longArrayOf(0, 800, 600, 800, 600, 800)
                lockscreenVisibility = Notification.VISIBILITY_PUBLIC
            },
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_INFO, c.getString(R.string.ch_info), NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = c.getString(R.string.ch_info_desc)
            },
        )
    }

    private fun open(c: Context, intent: Intent, code: Int): PendingIntent =
        PendingIntent.getActivity(c, code, intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)

    fun status(c: Context, text: String): Notification =
        NotificationCompat.Builder(c, CH_STATUS)
            .setSmallIcon(R.drawable.ic_bell)
            .setContentTitle(c.getString(R.string.app_name))
            .setContentText(text)
            .setOngoing(true)
            .setSilent(true)
            .setShowWhen(false)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setContentIntent(open(c, Intent(c, MainActivity::class.java), 0))
            .build()

    fun showRing(c: Context, id: String, time: Long) {
        val call = CallActivity.ringIntent(c, id)
        // Accepting opens the door view, so it goes to the activity, which tells the service.
        val accept = open(c, CallActivity.ringIntent(c, id, accept = true), 10)
        val reject = PendingIntent.getService(
            c, 11, DoorbellService.command(c, DoorbellService.ACTION_REJECT, id),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val n = NotificationCompat.Builder(c, CH_RING)
            .setSmallIcon(R.drawable.ic_bell)
            .setContentTitle(c.getString(R.string.ring_title))
            .setContentText(c.getString(R.string.ring_text, timeText(time)))
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setOngoing(true)
            .setAutoCancel(false)
            .setTimeoutAfter(120_000)
            .setContentIntent(open(c, call, 1))
            .setFullScreenIntent(open(c, call, 2), true)
            .addAction(R.drawable.ic_reject, c.getString(R.string.reject), reject)
            .addAction(R.drawable.ic_accept, c.getString(R.string.accept), accept)
            .build()
        n.flags = n.flags or Notification.FLAG_INSISTENT
        c.getSystemService(NotificationManager::class.java).notify(ID_RING, n)
    }

    fun cancelRing(c: Context) = c.getSystemService(NotificationManager::class.java).cancel(ID_RING)

    fun showMissed(c: Context, time: Long, by: String?) {
        val text = if (by.isNullOrEmpty()) c.getString(R.string.missed_text, timeText(time))
        else c.getString(R.string.taken_text, timeText(time), by)
        val n = NotificationCompat.Builder(c, CH_INFO)
            .setSmallIcon(R.drawable.ic_bell)
            .setContentTitle(c.getString(if (by.isNullOrEmpty()) R.string.missed_title else R.string.taken_title))
            .setContentText(text)
            .setAutoCancel(true)
            .setContentIntent(open(c, Intent(c, MainActivity::class.java), 3))
            .build()
        c.getSystemService(NotificationManager::class.java).notify(ID_MISSED, n)
    }

    fun showMessage(c: Context, time: Long) {
        val n = NotificationCompat.Builder(c, CH_INFO)
            .setSmallIcon(R.drawable.ic_bell)
            .setContentTitle(c.getString(R.string.message_title))
            .setContentText(c.getString(R.string.message_text, timeText(time)))
            .setAutoCancel(true)
            .setContentIntent(open(c, Intent(c, MessagesActivity::class.java), 4))
            .build()
        c.getSystemService(NotificationManager::class.java).notify(ID_MESSAGE, n)
    }

    fun timeText(seconds: Long): String {
        val ms = if (seconds > 0) seconds * 1000 else System.currentTimeMillis()
        return DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(ms))
    }
}
