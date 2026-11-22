package io.github.jacob3141.doorbell

import android.content.Intent
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import com.google.android.material.button.MaterialButton
import java.net.InetAddress
import java.util.concurrent.Executors

/**
 * Pairs the phone with the hub: the app asks for access, the admin approves it in the web
 * interface of the hub, and the app receives its token. Hubs in the Wi-Fi are found by mDNS.
 */
class SetupActivity : AppCompatActivity() {
    private val main = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()
    private lateinit var prefs: Prefs
    private lateinit var form: View
    private lateinit var waiting: View
    private lateinit var address: EditText
    private lateinit var name: EditText
    private lateinit var error: TextView
    private lateinit var found: LinearLayout
    private lateinit var searching: TextView
    private lateinit var request: Button
    private lateinit var code: TextView
    private lateinit var waitText: TextView
    private var asked: Registration? = null
    private var base: String? = null
    private var nsd: NsdManager? = null
    private var discovery: NsdManager.DiscoveryListener? = null
    private val resolving = ArrayDeque<NsdServiceInfo>()
    private var resolveBusy = false
    private val shown = HashSet<String>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        setContentView(R.layout.activity_setup)
        form = findViewById(R.id.setup_form)
        waiting = findViewById(R.id.setup_waiting)
        address = findViewById(R.id.setup_address)
        name = findViewById(R.id.setup_name)
        error = findViewById(R.id.setup_error)
        found = findViewById(R.id.setup_found)
        searching = findViewById(R.id.setup_searching)
        request = findViewById(R.id.setup_request)
        code = findViewById(R.id.setup_code)
        waitText = findViewById(R.id.setup_wait_text)

        address.setText(prefs.hub?.removePrefix("http://")?.removeSuffix(":${HubAddress.DEFAULT_PORT}").orEmpty())
        name.setText(prefs.deviceName ?: deviceName())
        request.setOnClickListener { ask() }
        findViewById<Button>(R.id.setup_cancel).setOnClickListener { cancel() }
        findViewById<Button>(R.id.setup_token).setOnClickListener { enterToken() }
    }

    override fun onStart() {
        super.onStart()
        startDiscovery()
    }

    override fun onStop() {
        stopDiscovery()
        super.onStop()
    }

    override fun onDestroy() {
        main.removeCallbacksAndMessages(null)
        io.shutdownNow()
        super.onDestroy()
    }

    private fun deviceName(): String =
        Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME)?.takeIf { it.isNotBlank() } ?: Build.MODEL

    private fun showError(text: String?) {
        error.text = text.orEmpty()
        error.visibility = if (text == null) View.GONE else View.VISIBLE
    }

    private fun inputs(): Pair<String, String>? {
        val b = HubAddress.normalize(address.text.toString())
        if (b == null) {
            showError(getString(R.string.err_address))
            return null
        }
        val n = name.text.toString().trim()
        if (n.isEmpty() || n.length > 64) {
            showError(getString(R.string.err_bad_name))
            return null
        }
        return b to n
    }

    private fun ask() {
        val (b, n) = inputs() ?: return
        showError(null)
        request.isEnabled = false
        io.execute {
            try {
                val r = HubApi.forWifi(b).register(n)
                main.post {
                    request.isEnabled = true
                    base = b
                    asked = r
                    prefs.deviceName = n
                    code.text = r.code.chunked(3).joinToString(" ")
                    waitText.text = getString(R.string.setup_wait_text, b)
                    form.visibility = View.GONE
                    waiting.visibility = View.VISIBLE
                    poll(r)
                }
            } catch (e: HubException) {
                main.post {
                    request.isEnabled = true
                    showError(e.text(this))
                }
            }
        }
    }

    private fun poll(r: Registration) {
        main.postDelayed({
            if (asked != r) return@postDelayed
            val b = base ?: return@postDelayed
            io.execute {
                val result = try {
                    HubApi.forWifi(b).poll(r)
                } catch (e: HubException) {
                    e
                }
                main.post {
                    if (asked != r) return@post
                    when (result) {
                        HubApi.PENDING -> poll(r)
                        HubApi.DENIED -> fail(getString(R.string.setup_denied))
                        is String -> paired(b, result, prefs.deviceName ?: deviceName())
                        is HubException -> if (result.reason == HubException.Reason.UNREACHABLE || result.reason == HubException.Reason.NO_WIFI) {
                            poll(r) // keep waiting, Wi-Fi may come back
                        } else {
                            fail(result.text(this))
                        }
                    }
                }
            }
        }, 2000)
    }

    private fun fail(text: String) {
        asked = null
        waiting.visibility = View.GONE
        form.visibility = View.VISIBLE
        showError(text)
    }

    private fun cancel() {
        asked = null
        waiting.visibility = View.GONE
        form.visibility = View.VISIBLE
    }

    private fun paired(b: String, token: String, n: String) {
        prefs.pair(b, token, n)
        DoorbellService.start(this)
        startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP))
        finish()
    }

    private fun enterToken() {
        val (b, n) = inputs() ?: return
        val field = EditText(this).apply {
            setSingleLine()
            hint = getString(R.string.setup_token_hint)
        }
        val pad = (20 * resources.displayMetrics.density).toInt()
        val box = LinearLayout(this).apply {
            setPadding(pad, pad / 2, pad, 0)
            addView(field, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT))
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.setup_token)
            .setMessage(R.string.setup_token_text)
            .setView(box)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val token = field.text.toString().trim()
                if (token.isEmpty()) return@setPositiveButton
                io.execute {
                    try {
                        HubApi.forWifi(b).messages(token) // checks the token
                        main.post { paired(b, token, n) }
                    } catch (e: HubException) {
                        main.post { showError(e.text(this)) }
                    }
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    // --- finding hubs in the Wi-Fi (the hub announces _doorbell-hub._tcp) ---

    private fun startDiscovery() {
        val m = getSystemService(NsdManager::class.java) ?: return
        nsd = m
        val l = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {}
            override fun onDiscoveryStopped(serviceType: String) {}
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                main.post { searching.visibility = View.GONE }
            }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
            override fun onServiceFound(info: NsdServiceInfo) {
                main.post {
                    resolving.addLast(info)
                    resolveNext()
                }
            }
            override fun onServiceLost(info: NsdServiceInfo) {}
        }
        discovery = l
        try {
            m.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, l)
            searching.visibility = View.VISIBLE
        } catch (e: Exception) {
            searching.visibility = View.GONE
        }
    }

    private fun stopDiscovery() {
        val l = discovery ?: return
        discovery = null
        try {
            nsd?.stopServiceDiscovery(l)
        } catch (e: Exception) {
            // already stopped
        }
    }

    @Suppress("DEPRECATION") // resolveService works on all versions we support
    private fun resolveNext() {
        val m = nsd ?: return
        if (resolveBusy) return
        val info = resolving.removeFirstOrNull() ?: return
        resolveBusy = true
        m.resolveService(info, object : NsdManager.ResolveListener {
            override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                main.post {
                    resolveBusy = false
                    resolveNext()
                }
            }

            override fun onServiceResolved(s: NsdServiceInfo) {
                val ip = s.host
                io.execute {
                    // Keep the DNS name of the hub, not its address, if the home network has one
                    val host = ip?.let { nameOf(it) ?: it.hostAddress }
                    main.post {
                        resolveBusy = false
                        if (host != null) addFound(s.serviceName, host, s.port)
                        resolveNext()
                    }
                }
            }
        })
    }

    /** The name the DNS of the home network gives [ip], if that name leads back to [ip]. */
    private fun nameOf(ip: InetAddress): String? = try {
        val name = InetAddress.getByAddress(ip.address).canonicalHostName.trimEnd('.')
        val n = WifiNet.network
        val back = if (n != null) n.getAllByName(name) else InetAddress.getAllByName(name)
        if (name != ip.hostAddress && back.any { it == ip }) name else null
    } catch (e: Exception) {
        null
    }

    private fun addFound(service: String, host: String, port: Int) {
        val shownHost = if (host.contains(':')) "[$host]" else host
        val addr = if (port == HubAddress.DEFAULT_PORT) shownHost else "$shownHost:$port"
        if (!shown.add(addr)) return
        searching.visibility = View.GONE
        val b = MaterialButton(this, null, com.google.android.material.R.attr.materialButtonOutlinedStyle).apply {
            text = getString(R.string.setup_found_item, service, addr)
            isAllCaps = false
            setOnClickListener { address.setText(addr) }
        }
        found.addView(b, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT))
        if (address.text.isNullOrBlank()) address.setText(addr)
    }

    companion object {
        private const val SERVICE_TYPE = "_doorbell-hub._tcp"
    }
}
