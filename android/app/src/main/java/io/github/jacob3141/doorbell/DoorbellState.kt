package io.github.jacob3141.doorbell

import android.os.Handler
import android.os.Looper

/** State shared by the service and the screens. Changed and read on the main thread. */
object DoorbellState {
    enum class Link { STOPPED, NO_WIFI, CONNECTING, CONNECTED, UNREACHABLE, NOT_PAIRED }

    enum class RingState { RINGING, ANSWERING, ANSWERED, TAKEN, MISSED, REJECTED }

    data class Ring(val id: String, val time: Long, val state: RingState, val by: String = "")

    private val main = Handler(Looper.getMainLooper())
    private val listeners = LinkedHashSet<() -> Unit>()

    var link: Link = Link.STOPPED
        private set

    /** The current or last ring. */
    var ring: Ring? = null
        private set

    fun setLink(l: Link) = update { link = l }

    fun setRing(r: Ring?) = update { ring = r }

    private fun update(change: () -> Unit) {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            main.post { update(change) }
            return
        }
        change()
        listeners.toList().forEach { it() }
    }

    fun listen(l: () -> Unit) {
        listeners += l
    }

    fun unlisten(l: () -> Unit) {
        listeners -= l
    }
}
