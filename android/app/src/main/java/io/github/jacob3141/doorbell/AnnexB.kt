package io.github.jacob3141.doorbell

/** Helpers for H.264 in Annex B form: NAL units separated by 00 00 01 or 00 00 00 01. */
object AnnexB {
    const val SLICE_IDR = 5
    const val SPS = 7
    const val PPS = 8

    private val START = byteArrayOf(0, 0, 0, 1)

    /** Returns the NAL units of [b] as index ranges without their start codes. */
    fun units(b: ByteArray): List<IntRange> {
        val starts = ArrayList<Int>() // index of the first byte after each start code
        var i = 0
        while (i + 2 < b.size) {
            if (b[i].toInt() == 0 && b[i + 1].toInt() == 0 && b[i + 2].toInt() == 1) {
                starts += i + 3
                i += 3
            } else {
                i++
            }
        }
        val out = ArrayList<IntRange>(starts.size)
        for ((n, s) in starts.withIndex()) {
            var end = if (n + 1 < starts.size) starts[n + 1] - 3 else b.size
            // A four-byte start code leaves one zero in front of the next three-byte match.
            while (n + 1 < starts.size && end > s && b[end - 1].toInt() == 0) end--
            if (end > s) out += s until end
        }
        return out
    }

    fun type(b: ByteArray, r: IntRange): Int = b[r.first].toInt() and 0x1f

    fun isKeyframe(b: ByteArray): Boolean = units(b).any { type(b, it) == SLICE_IDR }

    /** SPS and PPS of a keyframe, each with a start code, as the decoder wants them in csd-0 and csd-1. */
    fun parameterSets(b: ByteArray): Pair<ByteArray, ByteArray>? {
        var sps: ByteArray? = null
        var pps: ByteArray? = null
        for (r in units(b)) {
            when (type(b, r)) {
                SPS -> if (sps == null) sps = START + b.copyOfRange(r.first, r.last + 1)
                PPS -> if (pps == null) pps = START + b.copyOfRange(r.first, r.last + 1)
            }
        }
        val s = sps ?: return null
        val p = pps ?: return null
        return s to p
    }
}
