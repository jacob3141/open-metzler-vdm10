package io.github.jacob3141.doorbell

import android.media.MediaCodec
import android.media.MediaFormat
import android.util.Log
import android.view.Surface
import java.nio.ByteBuffer
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

/**
 * Decodes H.264 Annex B frames from the hub onto a surface with the hardware decoder.
 * Frames are shown as soon as they are decoded: this is live video, latency matters more than
 * smoothness. When the decoder falls behind, the queue is dropped up to the next keyframe.
 */
class H264Player(
    private val surface: Surface,
    private val onSize: (width: Int, height: Int) -> Unit,
    private val onFirstFrame: () -> Unit,
) {
    private val queue = LinkedBlockingQueue<ByteArray>(MAX_QUEUE)
    @Volatile
    private var running = true
    @Volatile
    private var waitKey = true
    private val thread = Thread(::run, "h264").apply { start() }

    /** Called from the network thread for every frame. */
    fun offer(frame: ByteArray) {
        if (waitKey) {
            if (!AnnexB.isKeyframe(frame)) return
            waitKey = false
        }
        if (!queue.offer(frame)) {
            // The decoder cannot keep up: start again at the next keyframe.
            queue.clear()
            waitKey = !AnnexB.isKeyframe(frame)
            if (!waitKey) queue.offer(frame)
        }
    }

    fun release() {
        running = false
        thread.interrupt()
        thread.join(1000)
    }

    private fun run() {
        var codec: MediaCodec? = null
        var params: Pair<ByteArray, ByteArray>? = null
        var first = true
        val info = MediaCodec.BufferInfo()
        val t0 = System.nanoTime()
        try {
            while (running) {
                val frame = queue.poll(20, TimeUnit.MILLISECONDS)
                if (frame != null) {
                    val ps = if (AnnexB.isKeyframe(frame)) AnnexB.parameterSets(frame) else null
                    if (ps != null && (params == null || !ps.first.contentEquals(params.first) || !ps.second.contentEquals(params.second))) {
                        codec?.let { it.stop(); it.release() }
                        codec = open(ps)
                        params = ps
                    }
                    val c = codec
                    if (c != null) {
                        val i = c.dequeueInputBuffer(50_000)
                        if (i >= 0) {
                            val buf: ByteBuffer = c.getInputBuffer(i)!!
                            buf.clear()
                            if (frame.size <= buf.remaining()) {
                                buf.put(frame)
                                c.queueInputBuffer(i, 0, frame.size, (System.nanoTime() - t0) / 1000, 0)
                            } else {
                                Log.w(TAG, "frame of ${frame.size} bytes does not fit")
                                c.queueInputBuffer(i, 0, 0, 0, 0)
                                waitKey = true
                            }
                        }
                    }
                }
                val c = codec ?: continue
                while (true) {
                    val o = c.dequeueOutputBuffer(info, 0)
                    when {
                        o >= 0 -> {
                            c.releaseOutputBuffer(o, true)
                            if (first) {
                                first = false
                                onFirstFrame()
                            }
                        }
                        o == MediaCodec.INFO_OUTPUT_FORMAT_CHANGED -> {
                            val f = c.outputFormat
                            var w = f.getInteger(MediaFormat.KEY_WIDTH)
                            var h = f.getInteger(MediaFormat.KEY_HEIGHT)
                            if (f.containsKey("crop-right") && f.containsKey("crop-left")) {
                                w = f.getInteger("crop-right") - f.getInteger("crop-left") + 1
                            }
                            if (f.containsKey("crop-bottom") && f.containsKey("crop-top")) {
                                h = f.getInteger("crop-bottom") - f.getInteger("crop-top") + 1
                            }
                            onSize(w, h)
                        }
                        else -> break
                    }
                }
            }
        } catch (e: InterruptedException) {
            // released
        } catch (e: Exception) {
            Log.e(TAG, "decoder failed", e)
        } finally {
            try {
                codec?.stop()
            } catch (e: Exception) {
                // already broken
            }
            codec?.release()
        }
    }

    private fun open(ps: Pair<ByteArray, ByteArray>): MediaCodec {
        val f = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, 1920, 1080)
        f.setByteBuffer("csd-0", ByteBuffer.wrap(ps.first))
        f.setByteBuffer("csd-1", ByteBuffer.wrap(ps.second))
        f.setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, 2 * 1024 * 1024)
        f.setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
        val c = MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
        c.configure(f, surface, null, 0)
        c.start()
        return c
    }

    companion object {
        private const val TAG = "H264Player"
        private const val MAX_QUEUE = 30
    }
}
