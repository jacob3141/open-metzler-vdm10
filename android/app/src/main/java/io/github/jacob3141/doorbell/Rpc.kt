package io.github.jacob3141.doorbell

import org.json.JSONObject

/** What the hub sends on /api/events (JSON-RPC 2.0). */
sealed class HubEvent {
    data class Ring(val id: String, val time: Long) : HubEvent()
    data class RingTaken(val id: String, val by: String) : HubEvent()
    data class RingMissed(val id: String) : HubEvent()
    data class MessageNew(val id: String, val time: Long) : HubEvent()
    data class Response(val id: Long, val ok: Boolean, val error: String?) : HubEvent()
    data object Unknown : HubEvent()
}

object Rpc {
    fun parse(text: String): HubEvent = try {
        val m = JSONObject(text)
        val p = m.optJSONObject("params") ?: JSONObject()
        when (m.optString("method")) {
            "ring" -> HubEvent.Ring(p.getString("id"), p.optLong("time"))
            "ring.taken" -> HubEvent.RingTaken(p.getString("id"), p.optString("by"))
            "ring.missed" -> HubEvent.RingMissed(p.getString("id"))
            "message.new" -> HubEvent.MessageNew(p.getString("id"), p.optLong("time"))
            "" -> if (m.has("id") && !m.isNull("id")) {
                val err = m.optJSONObject("error")
                HubEvent.Response(m.getLong("id"), err == null, err?.optString("message"))
            } else {
                HubEvent.Unknown
            }
            else -> HubEvent.Unknown
        }
    } catch (e: Exception) {
        HubEvent.Unknown
    }

    fun request(id: Long, method: String, ringId: String? = null): String {
        val m = JSONObject().put("jsonrpc", "2.0").put("id", id).put("method", method)
        if (ringId != null) m.put("params", JSONObject().put("id", ringId))
        return m.toString()
    }
}

/** Turns what the user typed into the base URL of the hub, e.g. "doorbell-hub.home.arpa" → "http://doorbell-hub.home.arpa". */
object HubAddress {
    const val DEFAULT_PORT = 80

    fun normalize(input: String): String? {
        var s = input.trim().trimEnd('/')
        if (s.isEmpty()) return null
        s = s.removePrefix("http://")
        if (s.startsWith("https://")) return null // the hub speaks plain HTTP in the home network
        if (s.contains('/') || s.contains('@') || s.contains(' ')) return null
        val host: String
        val port: Int
        if (s.startsWith("[")) { // IPv6 literal
            val end = s.indexOf(']')
            if (end < 0) return null
            host = s.substring(0, end + 1)
            val rest = s.substring(end + 1)
            port = if (rest.isEmpty()) DEFAULT_PORT else rest.removePrefix(":").toIntOrNull() ?: return null
        } else if (s.count { it == ':' } == 1) {
            host = s.substringBefore(':')
            port = s.substringAfter(':').toIntOrNull() ?: return null
        } else if (s.contains(':')) {
            return null
        } else {
            host = s
            port = DEFAULT_PORT
        }
        if (host.isEmpty() || port !in 1..65535) return null
        return if (port == DEFAULT_PORT) "http://$host" else "http://$host:$port"
    }

    fun ws(base: String, path: String): String = "ws" + base.removePrefix("http") + path
}
