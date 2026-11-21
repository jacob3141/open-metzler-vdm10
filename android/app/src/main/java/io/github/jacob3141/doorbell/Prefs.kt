package io.github.jacob3141.doorbell

import android.content.Context
import android.content.SharedPreferences

/** The pairing with the hub. The token stays in the app's private storage. */
class Prefs(context: Context) {
    private val p: SharedPreferences = context.getSharedPreferences("hub", Context.MODE_PRIVATE)

    var hub: String?
        get() = p.getString("hub", null)
        set(v) = p.edit().putString("hub", v).apply()

    var token: String?
        get() = p.getString("token", null)
        set(v) = p.edit().putString("token", v).apply()

    var deviceName: String?
        get() = p.getString("name", null)
        set(v) = p.edit().putString("name", v).apply()

    val paired: Boolean get() = !hub.isNullOrEmpty() && !token.isNullOrEmpty()

    fun pair(hub: String, token: String, name: String) {
        p.edit().putString("hub", hub).putString("token", token).putString("name", name).commit()
    }

    fun forget() {
        p.edit().remove("token").commit()
    }
}
