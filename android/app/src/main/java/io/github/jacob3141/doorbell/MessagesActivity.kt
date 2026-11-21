package io.github.jacob3141.doorbell

import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.format.DateUtils
import android.text.format.Formatter
import android.view.View
import android.widget.ArrayAdapter
import android.widget.ListView
import android.widget.ProgressBar
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import java.util.concurrent.Executors

/** The visitor messages that the hub recorded when nobody answered. */
class MessagesActivity : AppCompatActivity() {
    private val main = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()
    private lateinit var list: ListView
    private lateinit var empty: TextView
    private lateinit var progress: ProgressBar
    private var messages: List<VisitorMessage> = emptyList()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_messages)
        supportActionBar?.setDisplayHomeAsUpEnabled(true)
        list = findViewById(R.id.messages_list)
        empty = findViewById(R.id.messages_empty)
        progress = findViewById(R.id.messages_progress)
        list.setOnItemClickListener { _, _, pos, _ ->
            messages.getOrNull(pos)?.let { startActivity(Intent(this, PlayerActivity::class.java).putExtra(PlayerActivity.EXTRA_ID, it.id)) }
        }
    }

    override fun onStart() {
        super.onStart()
        load()
    }

    override fun onDestroy() {
        io.shutdownNow()
        super.onDestroy()
    }

    override fun onSupportNavigateUp(): Boolean {
        finish()
        return true
    }

    private fun load() {
        val prefs = Prefs(this)
        val hub = prefs.hub ?: return
        val token = prefs.token ?: return
        progress.visibility = View.VISIBLE
        io.execute {
            val result: Any = try {
                HubApi.forWifi(hub).messages(token)
            } catch (e: HubException) {
                e
            }
            main.post {
                progress.visibility = View.GONE
                if (result is HubException) {
                    show(emptyList(), result.text(this))
                } else {
                    @Suppress("UNCHECKED_CAST")
                    show(result as List<VisitorMessage>, getString(R.string.messages_none))
                }
            }
        }
    }

    private fun show(m: List<VisitorMessage>, emptyText: String) {
        messages = m
        empty.text = emptyText
        empty.visibility = if (m.isEmpty()) View.VISIBLE else View.GONE
        val rows = m.map {
            val time = if (it.time > 0) {
                DateUtils.formatDateTime(this, it.time, DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_SHOW_TIME or DateUtils.FORMAT_SHOW_WEEKDAY or DateUtils.FORMAT_ABBREV_ALL)
            } else {
                it.id
            }
            "$time  ·  ${Formatter.formatShortFileSize(this, it.size)}"
        }
        list.adapter = ArrayAdapter(this, android.R.layout.simple_list_item_1, rows)
    }
}
