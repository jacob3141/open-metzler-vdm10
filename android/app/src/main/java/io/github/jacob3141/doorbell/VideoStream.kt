package io.github.jacob3141.doorbell

import android.os.Handler
import android.os.Looper
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString

/**
 * Receives the door camera from /api/video (one binary message per H.264 frame) and passes the
 * frames on. Reconnects by itself until [stop].
 */
class VideoStream(
    private val hub: String,
    private val token: String,
    private val onFrame: (ByteArray) -> Unit,
    private val onProblem: (HubException.Reason?) -> Unit, // null: connected again
) {
    private val main = Handler(Looper.getMainLooper())
    private var socket: WebSocket? = null
    private var stopped = false
    private var gen = 0

    fun start() {
        if (stopped) return
        val client = WifiNet.client()
        if (client == null) {
            onProblem(HubException.Reason.NO_WIFI)
            retry()
            return
        }
        val g = ++gen
        socket = client.newWebSocket(
            HubApi.authed(HubAddress.ws(hub, "/api/video"), token).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    main.post { if (g == gen) onProblem(null) }
                }

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    if (g == gen) onFrame(bytes.toByteArray())
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    main.post {
                        if (g != gen || stopped) return@post
                        onProblem(if (response?.code == 401) HubException.Reason.NOT_PAIRED else HubException.Reason.UNREACHABLE)
                        if (response?.code != 401) retry()
                    }
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    main.post { if (g == gen && !stopped) retry() }
                }
            },
        )
    }

    private fun retry() {
        main.postDelayed({ if (!stopped) start() }, 2000)
    }

    fun stop() {
        stopped = true
        gen++
        main.removeCallbacksAndMessages(null)
        socket?.cancel()
        socket = null
    }
}
