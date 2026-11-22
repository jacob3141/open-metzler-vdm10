package main

import (
	"encoding/binary"
	"math"
)

// leveler brings the voice from the app to a steady loudness for the door speaker: it
// follows the peaks of the sound and scales them to the target, then applies the volume
// set by the admin. Phones send at very different levels, and a voice that is too quiet
// is lost in the street noise.
type leveler struct {
	env  float64 // peak envelope
	gain float64 // gain applied now, changes smoothly
}

const (
	levelTarget  = 0.7 * 32767 // where the peaks of a voice end up at full volume
	levelMaxGain = 12          // does not lift silence and the noise of the phone further
	levelFloor   = 150         // below this the sound counts as silence: no more gain
	levelRelease = 0.995       // per sample: the envelope falls by half in about 140 ms
	levelRise    = 1.0002      // per sample: the gain rises by about 3 dB per 0.2 s
)

// process levels 16-bit little-endian PCM in place. volume is 0..1.
func (l *leveler) process(pcm []byte, volume float64) {
	if l.gain == 0 {
		l.gain = 1
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		x := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		a := math.Abs(x)
		if a > l.env {
			l.env = a
		} else {
			l.env *= levelRelease
		}
		want := l.gain // silence: keep the gain, do not pump the noise up
		if l.env > levelFloor {
			want = math.Min(levelTarget/l.env, levelMaxGain)
		}
		if want < l.gain {
			l.gain = want // a loud start is turned down at once
		} else {
			l.gain = math.Min(l.gain*levelRise, want)
		}
		y := x * l.gain * volume
		if y > 32767 {
			y = 32767
		} else if y < -32768 {
			y = -32768
		}
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(y)))
	}
}
