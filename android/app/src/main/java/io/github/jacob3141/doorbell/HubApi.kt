package io.github.jacob3141.doorbell

import android.content.Context
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.IOException

/** A failed call to the hub. [text] gives the message in the user's language. */
class HubException(val reason: Reason, val code: Int = 0) : IOException(reason.name) {
    enum class Reason { UNREACHABLE, BAD_ANSWER, EXPIRED, NOT_PAIRED, TOO_MANY, BAD_NAME, NO_WIFI }

    fun text(c: Context): String = when (reason) {
        Reason.UNREACHABLE -> c.getString(R.string.err_unreachable)
        Reason.BAD_ANSWER -> c.getString(R.string.err_bad_answer, code)
        Reason.EXPIRED -> c.getString(R.string.err_expired)
        Reason.NOT_PAIRED -> c.getString(R.string.err_not_paired)
        Reason.TOO_MANY -> c.getString(R.string.err_too_many)
        Reason.BAD_NAME -> c.getString(R.string.err_bad_name)
        Reason.NO_WIFI -> c.getString(R.string.err_no_wifi)
    }
}

data class Registration(val id: String, val secret: String, val code: String)

data class VisitorMessage(val id: String, val time: Long, val size: Long)

/** The HTTP part of the hub API. All calls block, so run them off the main thread. */
class HubApi(private val base: String, private val client: OkHttpClient) {

    private fun call(req: Request): Response = try {
        client.newCall(req).execute()
    } catch (e: IOException) {
        throw HubException(HubException.Reason.UNREACHABLE)
    }

    private fun Response.json(): JSONObject = use {
        try {
            JSONObject(body?.string().orEmpty())
        } catch (e: Exception) {
            throw HubException(HubException.Reason.BAD_ANSWER, code)
        }
    }

    fun register(name: String): Registration {
        val body = JSONObject().put("name", name).toString().toRequestBody(JSON)
        val res = call(Request.Builder().url("$base/api/register").post(body).build())
        val code = res.code
        val m = res.json()
        when (code) {
            201 -> return Registration(m.getString("id"), m.getString("secret"), m.getString("code"))
            400 -> throw HubException(HubException.Reason.BAD_NAME)
            429 -> throw HubException(HubException.Reason.TOO_MANY)
            else -> throw HubException(HubException.Reason.BAD_ANSWER, code)
        }
    }

    /** Returns [PENDING], [DENIED] or the token. */
    fun poll(r: Registration): String {
        val res = call(Request.Builder().url("$base/api/register/${r.id}").header("Authorization", "Bearer ${r.secret}").build())
        if (res.code == 404) {
            res.close()
            throw HubException(HubException.Reason.EXPIRED)
        }
        val m = res.json()
        return when (m.optString("status")) {
            "approved" -> m.getString("token")
            "denied" -> DENIED
            else -> PENDING
        }
    }

    /** Checks a token by asking for the message list. */
    fun messages(token: String): List<VisitorMessage> {
        val res = call(authed("$base/api/messages", token).build())
        val code = res.code
        if (code == 401) {
            res.close()
            throw HubException(HubException.Reason.NOT_PAIRED)
        }
        val text = res.use { it.body?.string().orEmpty() }
        val a = try {
            JSONArray(text)
        } catch (e: Exception) {
            throw HubException(HubException.Reason.BAD_ANSWER, code)
        }
        return (0 until a.length()).map {
            val o = a.getJSONObject(it)
            VisitorMessage(o.getString("id"), parseTime(o.optString("time")), o.optLong("size"))
        }
    }

    fun download(token: String, id: String, to: File) {
        call(authed("$base/api/messages/$id", token).build()).use { res ->
            if (res.code == 401) throw HubException(HubException.Reason.NOT_PAIRED)
            if (!res.isSuccessful) throw HubException(HubException.Reason.BAD_ANSWER, res.code)
            to.outputStream().use { out -> res.body!!.byteStream().copyTo(out) }
        }
    }

    /** Removes this phone from the hub. */
    fun unregister(token: String) {
        call(authed("$base/api/client", token).delete().build()).close()
    }

    companion object {
        const val PENDING = "\u0000pending"
        const val DENIED = "\u0000denied"
        private val JSON = "application/json".toMediaType()

        fun authed(url: String, token: String): Request.Builder =
            Request.Builder().url(url).header("Authorization", "Bearer $token")

        /** Parses the RFC 3339 time of the hub; 0 if unknown. */
        fun parseTime(s: String): Long = try {
            java.time.OffsetDateTime.parse(s).toInstant().toEpochMilli()
        } catch (e: Exception) {
            0
        }

        /** An API for the paired hub through Wi-Fi. */
        fun forWifi(base: String): HubApi =
            HubApi(base, WifiNet.client() ?: throw HubException(HubException.Reason.NO_WIFI))
    }
}
