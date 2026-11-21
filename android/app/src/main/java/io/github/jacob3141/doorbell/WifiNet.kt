package io.github.jacob3141.doorbell

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Handler
import android.os.Looper
import okhttp3.Dns
import okhttp3.OkHttpClient
import java.util.concurrent.TimeUnit

/**
 * Tracks the Wi-Fi network. The app talks to the hub only through it, so it never tries over
 * mobile data, and it works even when Android prefers mobile data for the internet.
 */
object WifiNet {
    private val main = Handler(Looper.getMainLooper())
    private val listeners = LinkedHashSet<() -> Unit>()
    private val base: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    @Volatile
    var network: Network? = null
        private set

    @Volatile
    private var cached: Pair<Network, OkHttpClient>? = null

    fun start(context: Context) {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        val req = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        cm.registerNetworkCallback(req, object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(n: Network) = changed(n)
            override fun onLost(n: Network) {
                if (network == n) changed(null)
            }
        })
    }

    private fun changed(n: Network?) {
        main.post {
            if (network == n) return@post
            network = n
            listeners.toList().forEach { it() }
        }
    }

    /** Called on the main thread whenever Wi-Fi comes or goes. */
    fun listen(l: () -> Unit) = main.post { listeners += l }
    fun unlisten(l: () -> Unit) = main.post { listeners -= l }

    /** An HTTP client that goes through Wi-Fi, or null without Wi-Fi. */
    fun client(): OkHttpClient? {
        val n = network ?: return null
        cached?.let { if (it.first == n) return it.second }
        val c = base.newBuilder()
            .socketFactory(n.socketFactory)
            .dns(object : Dns {
                override fun lookup(hostname: String) = n.getAllByName(hostname).toList()
            })
            .build()
        cached = n to c
        return c
    }
}
