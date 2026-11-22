package io.github.jacob3141.doorbell

import android.annotation.SuppressLint
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import android.os.Handler
import android.os.Looper
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString.Companion.toByteString

/**
 * Sends the microphone of the phone to the speaker of the door station while the user holds the
 * talk button: 16 kHz mono 16-bit PCM in messages of 20 ms on /api/talk. [onEnd] runs on the main
 * thread when the connection ends before [stop], with the reason, or null when someone else is
 * talking at the door station already.
 */
class Talker(
    private val hub: String,
    private val token: String,
    private val onEnd: (HubException.Reason?) -> Unit,
) {
    private val main = Handler(Looper.getMainLooper())
    @Volatile private var stopped = false
    @Volatile private var socket: WebSocket? = null
    private var thread: Thread? = null

    /** Needs the RECORD_AUDIO permission, checked by the caller. */
    @SuppressLint("MissingPermission")
    fun start() {
        val client = WifiNet.client()
        if (client == null) {
            onEnd(HubException.Reason.NO_WIFI)
            return
        }
        val opened = System.currentTimeMillis()
        socket = client.newWebSocket(
            HubApi.authed(HubAddress.ws(hub, "/api/talk"), token).build(),
            object : WebSocketListener() {
                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    end(if (response?.code == 401) HubException.Reason.NOT_PAIRED else HubException.Reason.UNREACHABLE)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(1000, null)
                    // closed right away: the door station has a speaker already
                    end(if (System.currentTimeMillis() - opened < 3000) null else HubException.Reason.UNREACHABLE)
                }
            },
        )
        thread = Thread {
            val min = AudioRecord.getMinBufferSize(RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT)
            val rec = AudioRecord(
                MediaRecorder.AudioSource.VOICE_COMMUNICATION, RATE,
                AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT, maxOf(min, FRAME_BYTES * 10),
            )
            if (rec.state != AudioRecord.STATE_INITIALIZED) {
                rec.release()
                end(HubException.Reason.UNREACHABLE)
                return@Thread
            }
            val buf = ByteArray(FRAME_BYTES)
            rec.startRecording()
            try {
                while (!stopped) {
                    var n = 0
                    while (n < buf.size && !stopped) {
                        val r = rec.read(buf, n, buf.size - n)
                        if (r <= 0) break
                        n += r
                    }
                    if (n == buf.size) socket?.send(buf.toByteString())
                }
            } finally {
                rec.stop()
                rec.release()
            }
        }.apply {
            name = "talk"
            start()
        }
    }

    private fun end(reason: HubException.Reason?) {
        if (stopped) return
        stopped = true
        main.post { onEnd(reason) }
    }

    fun stop() {
        stopped = true
        socket?.close(1000, null)
        socket = null
    }

    companion object {
        private const val RATE = 16000
        private const val FRAME_BYTES = RATE / 50 * 2 // 20 ms
    }
}
