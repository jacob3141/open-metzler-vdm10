package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	videoWidth  = 1920
	videoHeight = 1080
	timescale   = 90000

	// The doorbell sends audio as 16-bit little-endian mono PCM, 20 ms per frame.
	audioRate        = 16000
	audioChunkFrames = audioRate / 50
)

// Frame is one H.264 picture in Annex B format. At is when the hub received it.
type Frame struct {
	Data []byte
	Key  bool
	At   time.Time
}

func splitNALs(b []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+3 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end--
				}
				out = append(out, b[start:end])
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func isKey(b []byte) bool {
	for _, n := range splitNALs(b) {
		if t := n[0] & 0x1f; t == 5 || t == 7 {
			return true
		}
	}
	return false
}

func box(name string, parts ...[]byte) []byte {
	n := 8
	for _, p := range parts {
		n += len(p)
	}
	b := make([]byte, 0, n)
	b = binary.BigEndian.AppendUint32(b, uint32(n))
	b = append(b, name...)
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

func u32s(v ...uint32) []byte {
	var b []byte
	for _, x := range v {
		b = binary.BigEndian.AppendUint32(b, x)
	}
	return b
}

func u16s(v ...uint16) []byte {
	var b []byte
	for _, x := range v {
		b = binary.BigEndian.AppendUint16(b, x)
	}
	return b
}

var matrix = u32s(0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000)

func avcC(sps, pps []byte) []byte {
	b := []byte{1, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	b = append(b, u16s(uint16(len(sps)))...)
	b = append(b, sps...)
	b = append(b, 1)
	b = append(b, u16s(uint16(len(pps)))...)
	b = append(b, pps...)
	return box("avcC", b)
}

// sample converts a frame to length-prefixed NAL units. SPS, PPS and access unit
// delimiters are left out of the sample and returned separately.
func sample(f Frame) (data, sps, pps []byte) {
	for _, n := range splitNALs(f.Data) {
		switch n[0] & 0x1f {
		case 7:
			sps = n
			continue
		case 8:
			pps = n
			continue
		case 9:
			continue
		}
		data = binary.BigEndian.AppendUint32(data, uint32(len(n)))
		data = append(data, n...)
	}
	return data, sps, pps
}

// codecString is the RFC 6381 name of the stream, e.g. avc1.4D0028.
func codecString(sps []byte) string {
	return fmt.Sprintf("avc1.%02X%02X%02X", sps[1], sps[2], sps[3])
}

func stsd(sps, pps []byte) []byte {
	avc1 := bytes.Join([][]byte{
		make([]byte, 6), u16s(1), make([]byte, 16),
		u16s(videoWidth, videoHeight), u32s(0x480000, 0x480000, 0), u16s(1),
		make([]byte, 32), u16s(0x18, 0xFFFF), avcC(sps, pps),
	}, nil)
	return box("stsd", u32s(0, 1), box("avc1", avc1))
}

func trak(dur uint32, stbl []byte) []byte {
	minf := box("minf",
		box("vmhd", u32s(1), make([]byte, 8)),
		box("dinf", box("dref", u32s(0, 1), box("url ", u32s(1)))),
		stbl,
	)
	mdia := box("mdia",
		box("mdhd", u32s(0, 0, 0, timescale, dur), u16s(0x55C4, 0)),
		box("hdlr", u32s(0, 0), []byte("vide"), make([]byte, 12), []byte("VideoHandler\x00")),
		minf,
	)
	return box("trak",
		box("tkhd", u32s(3, 0, 0, 1, 0, dur, 0, 0), u16s(0, 0, 0, 0), matrix, u32s(videoWidth<<16, videoHeight<<16)),
		mdia,
	)
}

func mvhd(dur, nextTrack uint32) []byte {
	return box("mvhd", u32s(0, 0, 0, timescale, dur, 0x10000), u16s(0x100, 0), make([]byte, 8), matrix, make([]byte, 24), u32s(nextTrack))
}

// audioTrak is track 2: PCM in a QuickTime "sowt" sample entry (16-bit little endian),
// which browsers and Android play without a decoder of their own. Each chunk holds
// audioChunkFrames samples; offsets lists where the chunks start in the file.
func audioTrak(samples uint32, offsets []uint32) []byte {
	sowt := bytes.Join([][]byte{
		make([]byte, 6), u16s(1), make([]byte, 8), u16s(1, 16, 0, 0), u32s(audioRate << 16),
	}, nil)
	stco := u32s(0, uint32(len(offsets)))
	for _, o := range offsets {
		stco = append(stco, u32s(o)...)
	}
	stbl := box("stbl",
		box("stsd", u32s(0, 1), box("sowt", sowt)),
		box("stts", u32s(0, 1, samples, 1)),
		box("stsc", u32s(0, 1, 1, audioChunkFrames, 1)),
		box("stsz", u32s(0, 2, samples)),
		box("stco", stco),
	)
	minf := box("minf",
		box("smhd", u32s(0, 0)),
		box("dinf", box("dref", u32s(0, 1), box("url ", u32s(1)))),
		stbl,
	)
	mdia := box("mdia",
		box("mdhd", u32s(0, 0, 0, audioRate, samples), u16s(0x55C4, 0)),
		box("hdlr", u32s(0, 0), []byte("soun"), make([]byte, 12), []byte("SoundHandler\x00")),
		minf,
	)
	dur := uint32(uint64(samples) * timescale / audioRate)
	return box("trak",
		box("tkhd", u32s(3, 0, 0, 2, 0, dur, 0, 0), u16s(0, 0, 0x100, 0), matrix, u32s(0, 0)),
		mdia,
	)
}

// WriteMP4 builds an MP4 file with the index at the start. The first frame must be a
// keyframe with SPS and PPS. audio is PCM that starts with the first frame, nil for none.
func WriteMP4(frames []Frame, audio []byte, seconds float64) ([]byte, error) {
	if len(frames) == 0 || !frames[0].Key {
		return nil, errors.New("mp4: first frame is not a keyframe")
	}
	var sps, pps []byte
	var mdat bytes.Buffer
	sizes := make([]uint32, 0, len(frames))
	var keys []uint32
	for i, f := range frames {
		d, s, p := sample(f)
		if s != nil {
			sps = s
		}
		if p != nil {
			pps = p
		}
		mdat.Write(d)
		sizes = append(sizes, uint32(len(d)))
		if f.Key {
			keys = append(keys, uint32(i+1))
		}
	}
	if len(sps) < 4 || len(pps) == 0 {
		return nil, errors.New("mp4: SPS or PPS missing")
	}
	delta := uint32(float64(timescale) * seconds / float64(len(frames)))
	if delta == 0 {
		delta = timescale / 30
	}
	dur := delta * uint32(len(frames))
	videoSize := uint32(mdat.Len())
	audio = audio[:len(audio)/(2*audioChunkFrames)*(2*audioChunkFrames)]
	mdat.Write(audio)
	samples := uint32(len(audio) / 2)

	ftyp := box("ftyp", []byte("isom"), u32s(0x200), []byte("isomiso2avc1mp41"))
	moov := func(offset uint32) []byte {
		stsz := u32s(0, 0, uint32(len(sizes)))
		for _, s := range sizes {
			stsz = append(stsz, u32s(s)...)
		}
		stss := u32s(0, uint32(len(keys)))
		for _, k := range keys {
			stss = append(stss, u32s(k)...)
		}
		stbl := box("stbl",
			stsd(sps, pps),
			box("stts", u32s(0, 1, uint32(len(frames)), delta)),
			box("stss", stss),
			box("stsc", u32s(0, 1, 1, uint32(len(frames)), 1)),
			box("stsz", stsz),
			box("stco", u32s(0, 1, offset)),
		)
		if samples == 0 {
			return box("moov", mvhd(dur, 2), trak(dur, stbl))
		}
		var chunks []uint32
		for o := uint32(0); o < samples*2; o += 2 * audioChunkFrames {
			chunks = append(chunks, offset+videoSize+o)
		}
		return box("moov", mvhd(dur, 3), trak(dur, stbl), audioTrak(samples, chunks))
	}
	head := uint32(len(ftyp) + len(moov(0)) + 8)
	return bytes.Join([][]byte{ftyp, moov(head), box("mdat", mdat.Bytes())}, nil), nil
}

// Fragmenter turns live frames into fragmented MP4 for Media Source Extensions:
// one init segment, then one moof/mdat pair per frame.
type Fragmenter struct {
	SPS, PPS []byte
	seq      uint32
	base     uint64
}

// NewFragmenter needs a keyframe that carries SPS and PPS.
func NewFragmenter(key Frame) (*Fragmenter, error) {
	_, sps, pps := sample(key)
	if !key.Key || len(sps) < 4 || len(pps) == 0 {
		return nil, errors.New("mp4: keyframe with SPS and PPS expected")
	}
	return &Fragmenter{SPS: sps, PPS: pps}, nil
}

// Init returns the init segment.
func (m *Fragmenter) Init() []byte {
	ftyp := box("ftyp", []byte("iso5"), u32s(0x200), []byte("iso5iso6avc1mp41"))
	stbl := box("stbl",
		stsd(m.SPS, m.PPS),
		box("stts", u32s(0, 0)),
		box("stsc", u32s(0, 0)),
		box("stsz", u32s(0, 0, 0)),
		box("stco", u32s(0, 0)),
	)
	mvex := box("mvex", box("trex", u32s(0, 1, 1, 0, 0, 0)))
	return bytes.Join([][]byte{ftyp, box("moov", mvhd(0, 2), trak(0, stbl), mvex)}, nil)
}

// Fragment returns one media segment holding f, shown for dur ticks of 1/90000 s.
// Decode times follow each other without gaps, so playback never stalls on a hole
// when frames were dropped.
func (m *Fragmenter) Fragment(f Frame, dur uint32) []byte {
	data, _, _ := sample(f)
	flags := uint32(0x01010000) // depends on other frames, not a sync sample
	if f.Key {
		flags = 0x02000000
	}
	m.seq++
	seq, base := m.seq, m.base
	m.base += uint64(dur)
	moof := func(offset uint32) []byte {
		return box("moof",
			box("mfhd", u32s(0, seq)),
			box("traf",
				box("tfhd", u32s(0x020000, 1)),
				box("tfdt", u32s(1<<24), binary.BigEndian.AppendUint64(nil, base)),
				box("trun", u32s(0x000701, 1, offset, dur, uint32(len(data)), flags)),
			),
		)
	}
	return append(moof(uint32(len(moof(0))+8)), box("mdat", data)...)
}
