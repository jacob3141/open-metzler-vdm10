package io.github.jacob3141.doorbell

import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.util.Log
import android.widget.Toast
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit

/**
 * Keeps the JSON-RPC connection to the hub (/api/events) while the phone is in Wi-Fi, and turns
 * rings into a full-screen call notification. Runs as a foreground service, like an intercom.
 */
class DoorbellService : Service() {
    private val main = Handler(Looper.getMainLooper())
    private lateinit var prefs: Prefs
    private var socket: WebSocket? = null
    private var generation = 0 // ignores callbacks of sockets that were replaced
    private var backoff = MIN_BACKOFF
    private var nextId = 1L
    private val pending = HashMap<Long, (Boolean) -> Unit>()
    private val onWifi = { reconnect(now = true) }
    private val onState = { showStatus() }
    private var shownLink: DoorbellState.Link? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        prefs = Prefs(this)
        val n = Notifications.status(this, getString(R.string.status_connecting))
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(Notifications.ID_STATUS, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(Notifications.ID_STATUS, n)
        }
        DoorbellState.listen(onState)
        WifiNet.listen(onWifi)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (!prefs.paired) {
            stopSelf()
            return START_NOT_STICKY
        }
        val ring = intent?.getStringExtra(EXTRA_RING)
        when (intent?.action) {
            ACTION_ACCEPT -> if (ring != null) accept(ring)
            ACTION_REJECT -> if (ring != null) reject(ring)
            ACTION_RECONNECT -> reconnect(now = true)
            ACTION_STOP_ALARM -> stopAlarm()
            else -> if (socket == null) reconnect(now = true)
        }
        return START_STICKY
    }

    override fun onDestroy() {
        WifiNet.unlisten(onWifi)
        DoorbellState.unlisten(onState)
        main.removeCallbacksAndMessages(null)
        generation++
        socket?.close(1000, null)
        socket = null
        DoorbellState.setLink(DoorbellState.Link.STOPPED)
        super.onDestroy()
    }

    private fun showStatus() {
        if (shownLink == DoorbellState.link) return
        shownLink = DoorbellState.link
        val text = when (DoorbellState.link) {
            DoorbellState.Link.CONNECTED -> getString(R.string.status_connected)
            DoorbellState.Link.NO_WIFI -> getString(R.string.status_no_wifi)
            DoorbellState.Link.UNREACHABLE -> getString(R.string.status_unreachable)
            DoorbellState.Link.NOT_PAIRED -> getString(R.string.status_not_paired)
            else -> getString(R.string.status_connecting)
        }
        getSystemService(NotificationManager::class.java).notify(Notifications.ID_STATUS, Notifications.status(this, text))
    }

    /** Closes the current connection and opens a new one, now or after the backoff. */
    private fun reconnect(now: Boolean) {
        main.removeCallbacks(connectRunnable)
        generation++
        socket?.cancel()
        socket = null
        failPending()
        if (now) {
            backoff = MIN_BACKOFF
            connect()
        } else {
            main.postDelayed(connectRunnable, backoff)
            backoff = (backoff * 2).coerceAtMost(MAX_BACKOFF)
        }
    }

    private val connectRunnable = Runnable { connect() }

    private fun connect() {
        val hub = prefs.hub
        val token = prefs.token
        if (hub == null || token == null) {
            stopSelf()
            return
        }
        val client = WifiNet.client()
        if (client == null) {
            DoorbellState.setLink(DoorbellState.Link.NO_WIFI)
            return // WifiNet calls back when Wi-Fi is there
        }
        DoorbellState.setLink(DoorbellState.Link.CONNECTING)
        val gen = ++generation
        val ws = client.newBuilder().pingInterval(PING_SECONDS, TimeUnit.SECONDS).build()
            .newWebSocket(HubApi.authed(HubAddress.ws(hub, "/api/events"), token).build(), Listener(gen))
        socket = ws
    }

    private inner class Listener(private val gen: Int) : WebSocketListener() {
        private fun onMain(f: () -> Unit) {
            main.post { if (gen == generation) f() }
        }

        override fun onOpen(webSocket: WebSocket, response: Response) = onMain {
            backoff = MIN_BACKOFF
            DoorbellState.setLink(DoorbellState.Link.CONNECTED)
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            val ev = Rpc.parse(text)
            if (ev is HubEvent.Ring || ev is HubEvent.Tamper) wakeBriefly()
            onMain { handle(ev) }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(1000, null)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = onMain { lost() }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = onMain {
            if (response?.code == 401) {
                Log.w(TAG, "the hub does not know this phone")
                DoorbellState.setLink(DoorbellState.Link.NOT_PAIRED)
                socket = null
                failPending()
                return@onMain
            }
            Log.i(TAG, "connection lost: ${t.message}")
            lost()
        }
    }

    private fun lost() {
        socket = null
        failPending()
        endRingWithoutAnswer()
        DoorbellState.setLink(if (WifiNet.network == null) DoorbellState.Link.NO_WIFI else DoorbellState.Link.UNREACHABLE)
        if (WifiNet.network != null) reconnect(now = false)
    }

    private fun handle(ev: HubEvent) {
        when (ev) {
            is HubEvent.Ring -> {
                DoorbellState.setRing(DoorbellState.Ring(ev.id, ev.time, DoorbellState.RingState.RINGING))
                Notifications.showRing(this, ev.id, ev.time)
                // On a locked phone the full-screen notification opens the door view. When the app
                // is open anyway, Android only shows a small notification, so open it here.
                if (App.visible > 0) startActivity(CallActivity.ringIntent(this, ev.id))
            }
            is HubEvent.RingTaken -> endRing(ev.id, DoorbellState.RingState.TAKEN, ev.by)
            is HubEvent.RingMissed -> endRing(ev.id, DoorbellState.RingState.MISSED, "")
            is HubEvent.MessageNew -> Notifications.showMessage(this, ev.time)
            is HubEvent.Tamper -> tamper(DoorbellState.Tamper(ev.open, ev.alarm, ev.time))
            is HubEvent.Response -> pending.remove(ev.id)?.invoke(ev.ok)
            HubEvent.Unknown -> {}
        }
    }

    private fun endRing(id: String, state: DoorbellState.RingState, by: String) {
        val r = DoorbellState.ring ?: return
        if (r.id != id || r.state != DoorbellState.RingState.RINGING) return
        Notifications.cancelRing(this)
        DoorbellState.setRing(r.copy(state = state, by = by))
        Notifications.showMissed(this, r.time, by.ifEmpty { null })
    }

    /** Without a connection the hub counts this phone as having declined. */
    private fun endRingWithoutAnswer() {
        val r = DoorbellState.ring ?: return
        if (r.state == DoorbellState.RingState.RINGING || r.state == DoorbellState.RingState.ANSWERING) {
            Notifications.cancelRing(this)
            DoorbellState.setRing(r.copy(state = DoorbellState.RingState.MISSED))
        }
    }

    /** A new alarm sounds on the phone; a change of the same alarm only updates the notification. */
    private fun tamper(t: DoorbellState.Tamper) {
        val before = DoorbellState.tamper
        if (t == before) return
        DoorbellState.setTamper(t)
        if (before == null && !t.open && !t.alarm) return
        val alert = (t.open && before?.open != true) || (t.alarm && before?.alarm != true)
        Notifications.showTamper(this, t, alert)
    }

    private fun stopAlarm() {
        send("alarm.stop", null) { ok ->
            if (!ok) Toast.makeText(this, R.string.alarm_stop_failed, Toast.LENGTH_LONG).show()
        }
    }

    private fun send(method: String, ring: String?, done: (Boolean) -> Unit) {
        val ws = socket
        if (ws == null || DoorbellState.link != DoorbellState.Link.CONNECTED) {
            done(false)
            return
        }
        val id = nextId++
        pending[id] = done
        if (!ws.send(Rpc.request(id, method, ring))) pending.remove(id)?.invoke(false)
    }

    private fun failPending() {
        val p = pending.values.toList()
        pending.clear()
        p.forEach { it(false) }
    }

    private fun accept(id: String) {
        Notifications.cancelRing(this)
        val r = DoorbellState.ring
        if (r == null || r.id != id || r.state != DoorbellState.RingState.RINGING) return
        DoorbellState.setRing(r.copy(state = DoorbellState.RingState.ANSWERING))
        send("ring.accept", id) { ok ->
            val now = DoorbellState.ring
            if (now?.id == id && now.state == DoorbellState.RingState.ANSWERING) {
                DoorbellState.setRing(now.copy(state = if (ok) DoorbellState.RingState.ANSWERED else DoorbellState.RingState.MISSED))
            }
        }
    }

    private fun reject(id: String) {
        Notifications.cancelRing(this)
        val r = DoorbellState.ring
        if (r == null || r.id != id || r.state != DoorbellState.RingState.RINGING) return
        DoorbellState.setRing(r.copy(state = DoorbellState.RingState.REJECTED))
        send("ring.reject", id) {}
    }

    private fun wakeBriefly() {
        val pm = getSystemService(PowerManager::class.java)
        pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "doorbell:ring").acquire(10_000)
    }

    companion object {
        private const val TAG = "DoorbellService"
        private const val MIN_BACKOFF = 2_000L
        private const val MAX_BACKOFF = 60_000L
        private const val PING_SECONDS = 60L // each ping wakes the phone; a lost hub shows within two minutes

        const val ACTION_ACCEPT = "io.github.jacob3141.doorbell.ACCEPT"
        const val ACTION_REJECT = "io.github.jacob3141.doorbell.REJECT"
        const val ACTION_RECONNECT = "io.github.jacob3141.doorbell.RECONNECT"
        const val ACTION_STOP_ALARM = "io.github.jacob3141.doorbell.STOP_ALARM"
        const val EXTRA_RING = "ring"

        fun command(c: Context, action: String, ring: String? = null): Intent =
            Intent(c, DoorbellService::class.java).setAction(action).apply { if (ring != null) putExtra(EXTRA_RING, ring) }

        fun start(c: Context) {
            if (Prefs(c).paired) c.startForegroundService(Intent(c, DoorbellService::class.java))
        }

        fun stop(c: Context) {
            c.stopService(Intent(c, DoorbellService::class.java))
        }
    }
}
