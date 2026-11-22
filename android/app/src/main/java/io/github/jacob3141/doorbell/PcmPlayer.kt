package io.github.jacob3141.doorbell

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack
import android.os.Build

/**
 * Plays the microphone of the door station: 16 kHz mono 16-bit PCM as the hub sends it on
 * /api/audio. The buffer holds [BUFFER_MS]; frames that arrive while it is full are dropped,
 * so the sound never lags further behind. Playback starts once [START_MS] are buffered
 * (Android 12 and newer; before, once the buffer is full).
 */
class PcmPlayer {
    private val track: AudioTrack
    private var playing = false
    private var released = false

    init {
        val min = AudioTrack.getMinBufferSize(RATE, AudioFormat.CHANNEL_OUT_MONO, AudioFormat.ENCODING_PCM_16BIT)
        track = AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build(),
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setSampleRate(RATE)
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
                    .build(),
            )
            .setBufferSizeInBytes(maxOf(min, RATE * BUFFER_MS / 1000 * 2))
            .setTransferMode(AudioTrack.MODE_STREAM)
            .build()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            track.setStartThresholdInFrames(RATE * START_MS / 1000)
        }
    }

    /** Takes one frame of PCM; called on the thread of the stream. */
    @Synchronized
    fun offer(pcm: ByteArray) {
        if (released || pcm.size < 2) return
        track.write(pcm, 0, pcm.size and 1.inv(), AudioTrack.WRITE_NON_BLOCKING)
        if (!playing) {
            track.play()
            playing = true
        }
    }

    @Synchronized
    fun release() {
        if (released) return
        released = true
        try {
            track.pause()
            track.flush()
        } catch (e: IllegalStateException) {
            // not playing yet
        }
        track.release()
    }

    companion object {
        const val RATE = 16000
        private const val START_MS = 80
        private const val BUFFER_MS = 250
    }
}
