package io.github.jacob3141.doorbell

import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.MediaController
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.VideoView
import androidx.appcompat.app.AppCompatActivity
import java.io.File
import java.util.concurrent.Executors

/** Plays one visitor message. It is downloaded through Wi-Fi first, then played from the cache. */
class PlayerActivity : AppCompatActivity() {
    private val main = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()
    private lateinit var video: VideoView
    private lateinit var progress: ProgressBar
    private lateinit var error: TextView
    private var file: File? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_player)
        video = findViewById(R.id.player_video)
        progress = findViewById(R.id.player_progress)
        error = findViewById(R.id.player_error)
        val id = intent.getStringExtra(EXTRA_ID) ?: return finish()
        if (!id.all { it.isDigit() || it == '-' }) return finish()
        val prefs = Prefs(this)
        val hub = prefs.hub ?: return finish()
        val token = prefs.token ?: return finish()
        val f = File(cacheDir, "message-$id.mp4")
        file = f
        io.execute {
            val problem = try {
                if (!f.exists()) {
                    val tmp = File(cacheDir, "message-$id.part")
                    HubApi.forWifi(hub).download(token, id, tmp)
                    tmp.renameTo(f)
                }
                null
            } catch (e: HubException) {
                e
            }
            main.post { if (problem == null) play(f) else showError(problem.text(this)) }
        }
    }

    private fun play(f: File) {
        progress.visibility = View.GONE
        val mc = MediaController(this)
        mc.setAnchorView(video)
        video.setMediaController(mc)
        video.setOnErrorListener { _, _, _ ->
            showError(getString(R.string.player_error))
            true
        }
        video.setVideoPath(f.path)
        video.start()
    }

    private fun showError(text: String) {
        progress.visibility = View.GONE
        error.text = text
        error.visibility = View.VISIBLE
    }

    override fun onDestroy() {
        io.shutdownNow()
        // Keep the cache small: only the message that was just watched stays.
        cacheDir.listFiles()?.filter { it.name.startsWith("message-") && it != file }?.forEach { it.delete() }
        super.onDestroy()
    }

    companion object {
        const val EXTRA_ID = "id"
    }
}
