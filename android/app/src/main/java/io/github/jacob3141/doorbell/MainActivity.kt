package io.github.jacob3141.doorbell

import android.Manifest
import android.annotation.SuppressLint
import android.app.NotificationManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.view.Menu
import android.view.MenuItem
import android.view.View
import android.widget.Button
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat

class MainActivity : AppCompatActivity() {
    private lateinit var prefs: Prefs
    private lateinit var status: TextView
    private lateinit var hubText: TextView
    private lateinit var retry: Button
    private lateinit var permNotify: View
    private lateinit var permFullScreen: View
    private lateinit var permBattery: View
    private lateinit var alarm: View
    private lateinit var alarmText: TextView
    private lateinit var alarmStop: Button
    private val onState = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        if (!prefs.paired) {
            startActivity(Intent(this, SetupActivity::class.java))
            finish()
            return
        }
        setContentView(R.layout.activity_main)
        status = findViewById(R.id.main_status)
        hubText = findViewById(R.id.main_hub)
        retry = findViewById(R.id.main_retry)
        permNotify = findViewById(R.id.perm_notify)
        permFullScreen = findViewById(R.id.perm_fullscreen)
        permBattery = findViewById(R.id.perm_battery)
        alarm = findViewById(R.id.main_alarm)
        alarmText = findViewById(R.id.main_alarm_text)
        alarmStop = findViewById(R.id.main_alarm_stop)
        alarmStop.setOnClickListener { startService(DoorbellService.command(this, DoorbellService.ACTION_STOP_ALARM)) }

        findViewById<Button>(R.id.main_live).setOnClickListener { startActivity(CallActivity.liveIntent(this)) }
        findViewById<Button>(R.id.main_messages).setOnClickListener { startActivity(Intent(this, MessagesActivity::class.java)) }
        retry.setOnClickListener {
            if (DoorbellState.link == DoorbellState.Link.NOT_PAIRED) unpair(askHub = false)
            else startService(DoorbellService.command(this, DoorbellService.ACTION_RECONNECT))
        }
        findViewById<Button>(R.id.perm_notify_button).setOnClickListener {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        findViewById<Button>(R.id.perm_fullscreen_button).setOnClickListener {
            if (Build.VERSION.SDK_INT >= 34) {
                startActivity(Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, Uri.parse("package:$packageName")))
            }
        }
        findViewById<Button>(R.id.perm_battery_button).setOnClickListener { askBattery() }
    }

    @SuppressLint("BatteryLife") // a doorbell has to stay connected, that is the point of the app
    private fun askBattery() {
        startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
    }

    override fun onStart() {
        super.onStart()
        if (!prefs.paired) return
        DoorbellService.start(this)
        DoorbellState.listen(onState)
        render()
    }

    override fun onResume() {
        super.onResume()
        if (prefs.paired) renderPermissions()
    }

    override fun onStop() {
        DoorbellState.unlisten(onState)
        super.onStop()
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        renderPermissions()
    }

    private fun render() {
        hubText.text = getString(R.string.main_hub, prefs.deviceName.orEmpty(), prefs.hub.orEmpty().removePrefix("http://"))
        status.text = when (DoorbellState.link) {
            DoorbellState.Link.CONNECTED -> getString(R.string.status_connected)
            DoorbellState.Link.NO_WIFI -> getString(R.string.status_no_wifi)
            DoorbellState.Link.UNREACHABLE -> getString(R.string.status_unreachable)
            DoorbellState.Link.NOT_PAIRED -> getString(R.string.status_not_paired)
            else -> getString(R.string.status_connecting)
        }
        status.setTextColor(
            ContextCompat.getColor(this, if (DoorbellState.link == DoorbellState.Link.CONNECTED) R.color.ok else R.color.bad),
        )
        retry.visibility = when (DoorbellState.link) {
            DoorbellState.Link.UNREACHABLE, DoorbellState.Link.NOT_PAIRED -> View.VISIBLE
            else -> View.GONE
        }
        retry.setText(if (DoorbellState.link == DoorbellState.Link.NOT_PAIRED) R.string.pair_again else R.string.retry)

        // The tamper alarm stays on screen while the station is off the wall or its siren sounds.
        val t = DoorbellState.tamper
        alarm.visibility = if (t != null && (t.open || t.alarm)) View.VISIBLE else View.GONE
        if (t != null) alarmText.text = Notifications.tamperText(this, t)
        alarmStop.visibility = if (t?.alarm == true) View.VISIBLE else View.GONE
    }

    private fun renderPermissions() {
        val notify = Build.VERSION.SDK_INT < 33 ||
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        permNotify.visibility = if (notify) View.GONE else View.VISIBLE
        val fullScreen = Build.VERSION.SDK_INT < 34 || getSystemService(NotificationManager::class.java).canUseFullScreenIntent()
        permFullScreen.visibility = if (fullScreen) View.GONE else View.VISIBLE
        val battery = getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName)
        permBattery.visibility = if (battery) View.GONE else View.VISIBLE
    }

    override fun onCreateOptionsMenu(menu: Menu): Boolean {
        menuInflater.inflate(R.menu.main, menu)
        return true
    }

    override fun onOptionsItemSelected(item: MenuItem): Boolean {
        if (item.itemId == R.id.menu_unpair) {
            AlertDialog.Builder(this)
                .setTitle(R.string.unpair_title)
                .setMessage(R.string.unpair_text)
                .setPositiveButton(R.string.unpair) { _, _ -> unpair(askHub = true) }
                .setNegativeButton(android.R.string.cancel, null)
                .show()
            return true
        }
        return super.onOptionsItemSelected(item)
    }

    private fun unpair(askHub: Boolean) {
        val hub = prefs.hub
        val token = prefs.token
        DoorbellService.stop(this)
        prefs.forget()
        if (askHub && hub != null && token != null) {
            Thread {
                try {
                    HubApi.forWifi(hub).unregister(token)
                } catch (e: HubException) {
                    // the admin can still remove it in the web interface
                }
            }.start()
        }
        startActivity(Intent(this, SetupActivity::class.java))
        finish()
    }
}
