package io.github.jacob3141.doorbell

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.view.WindowManager
import android.widget.Button
import android.widget.ProgressBar
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity

/**
 * The door camera: during a ring with accept and decline, or as a live view. The sound of the
 * door station plays in the live view and once the ring is answered, not while it rings.
 */
class CallActivity : AppCompatActivity() {
    private val main = Handler(Looper.getMainLooper())
    private lateinit var prefs: Prefs
    private lateinit var frame: AspectFrameLayout
    private lateinit var status: TextView
    private lateinit var progress: ProgressBar
    private lateinit var accept: Button
    private lateinit var reject: Button
    private lateinit var hangup: Button
    private var ringId: String? = null
    private var player: H264Player? = null
    private var stream: MediaStream? = null
    private var audio: MediaStream? = null
    private var speaker: PcmPlayer? = null
    private var started = false
    private val onState = { render() }
    private val close = Runnable { finish() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        if (!prefs.paired) {
            finish()
            return
        }
        setContentView(R.layout.activity_call)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        frame = findViewById(R.id.video_frame)
        status = findViewById(R.id.call_status)
        progress = findViewById(R.id.call_progress)
        accept = findViewById(R.id.call_accept)
        reject = findViewById(R.id.call_reject)
        hangup = findViewById(R.id.call_hangup)
        accept.setOnClickListener { ringId?.let { answer(it) } }
        reject.setOnClickListener {
            ringId?.let { startService(DoorbellService.command(this, DoorbellService.ACTION_REJECT, it)) }
            finish()
        }
        hangup.setOnClickListener { finish() }
        findViewById<SurfaceView>(R.id.video).holder.addCallback(object : SurfaceHolder.Callback {
            override fun surfaceCreated(holder: SurfaceHolder) = startVideo(holder)
            override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {}
            override fun surfaceDestroyed(holder: SurfaceHolder) = stopVideo()
        })
        take(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        take(intent)
    }

    private fun take(i: Intent) {
        ringId = i.getStringExtra(EXTRA_RING)
        val id = ringId
        if (id != null && i.getBooleanExtra(EXTRA_ACCEPT, false)) {
            i.removeExtra(EXTRA_ACCEPT) // not again after a rotation
            answer(id)
        }
        render()
    }

    private fun answer(id: String) {
        startService(DoorbellService.command(this, DoorbellService.ACTION_ACCEPT, id))
    }

    override fun onStart() {
        super.onStart()
        started = true
        DoorbellState.listen(onState)
        render()
    }

    override fun onStop() {
        DoorbellState.unlisten(onState)
        started = false
        syncAudio()
        super.onStop()
    }

    override fun onDestroy() {
        main.removeCallbacksAndMessages(null)
        stopVideo()
        stopAudio()
        super.onDestroy()
    }

    private fun render() {
        renderState()
        syncAudio()
    }

    private fun wantAudio(): Boolean {
        if (!started) return false
        val id = ringId ?: return true // live view
        val r = DoorbellState.ring
        return r != null && r.id == id &&
            (r.state == DoorbellState.RingState.ANSWERING || r.state == DoorbellState.RingState.ANSWERED)
    }

    private fun syncAudio() {
        if (wantAudio()) startAudio() else stopAudio()
    }

    private fun startAudio() {
        if (audio != null) return
        val hub = prefs.hub ?: return
        val token = prefs.token ?: return
        val p = PcmPlayer()
        speaker = p
        audio = MediaStream(hub, token, "/api/audio", p::offer) {}.also { it.start() }
    }

    private fun stopAudio() {
        audio?.stop()
        audio = null
        speaker?.release()
        speaker = null
    }

    private fun renderState() {
        val id = ringId
        val r = DoorbellState.ring
        main.removeCallbacks(close)
        if (id == null) { // live view
            show(accept = false, reject = false, hangup = true)
            return
        }
        if (r == null || r.id != id) {
            status.setText(R.string.call_over)
            show(accept = false, reject = false, hangup = true)
            return
        }
        when (r.state) {
            DoorbellState.RingState.RINGING -> {
                status.setText(R.string.call_ringing)
                show(accept = true, reject = true, hangup = false)
            }
            DoorbellState.RingState.ANSWERING -> {
                status.setText(R.string.call_answering)
                show(accept = false, reject = false, hangup = true)
            }
            DoorbellState.RingState.ANSWERED -> {
                status.setText(R.string.call_answered)
                show(accept = false, reject = false, hangup = true)
            }
            DoorbellState.RingState.TAKEN -> {
                status.text = getString(R.string.call_taken, r.by)
                show(accept = false, reject = false, hangup = true)
                main.postDelayed(close, 4000)
            }
            DoorbellState.RingState.MISSED -> {
                status.setText(R.string.call_over)
                show(accept = false, reject = false, hangup = true)
                main.postDelayed(close, 4000)
            }
            DoorbellState.RingState.REJECTED -> finish()
        }
    }

    private fun show(accept: Boolean, reject: Boolean, hangup: Boolean) {
        this.accept.visibility = if (accept) View.VISIBLE else View.GONE
        this.reject.visibility = if (reject) View.VISIBLE else View.GONE
        this.hangup.visibility = if (hangup) View.VISIBLE else View.GONE
        if (ringId == null && player == null) status.setText(R.string.call_connecting)
    }

    private fun startVideo(holder: SurfaceHolder) {
        val hub = prefs.hub ?: return
        val token = prefs.token ?: return
        progress.visibility = View.VISIBLE
        val p = H264Player(
            holder.surface,
            onSize = { w, h -> main.post { frame.setAspect(w, h) } },
            onFirstFrame = {
                main.post {
                    progress.visibility = View.GONE
                    if (ringId == null) status.setText(R.string.call_live)
                }
            },
        )
        player = p
        stream = MediaStream(hub, token, "/api/video", p::offer) { problem ->
            if (problem != null && ringId == null) status.text = HubException(problem).text(this)
        }.also { it.start() }
    }

    private fun stopVideo() {
        stream?.stop()
        stream = null
        player?.release()
        player = null
    }

    companion object {
        private const val EXTRA_RING = "ring"
        private const val EXTRA_ACCEPT = "accept"

        fun ringIntent(c: Context, id: String, accept: Boolean = false): Intent =
            Intent(c, CallActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                .putExtra(EXTRA_RING, id)
                .putExtra(EXTRA_ACCEPT, accept)
                .setAction(if (accept) "accept:$id" else "ring:$id") // keeps the pending intents apart

        fun liveIntent(c: Context): Intent = Intent(c, CallActivity::class.java)
    }
}
