package main

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	videoWidth  = 1920
	videoHeight = 1080
	timescale   = 90000
)

// Frame is one H.264 picture in Annex B format.
type Frame struct {
	Data []byte
	Key  bool
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

// WriteMP4 builds an MP4 file with the index at the start. The first frame must be a
// keyframe with SPS and PPS.
func WriteMP4(frames []Frame, seconds float64) ([]byte, error) {
	if len(frames) == 0 || !frames[0].Key {
		return nil, errors.New("mp4: first frame is not a keyframe")
	}
	var sps, pps []byte
	var mdat bytes.Buffer
	sizes := make([]uint32, 0, len(frames))
	var keys []uint32
	for i, f := range frames {
		var size uint32
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
			binary.Write(&mdat, binary.BigEndian, uint32(len(n)))
			mdat.Write(n)
			size += uint32(len(n)) + 4
		}
		sizes = append(sizes, size)
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
		avc1 := bytes.Join([][]byte{
			make([]byte, 6), u16s(1), make([]byte, 16),
			u16s(videoWidth, videoHeight), u32s(0x480000, 0x480000, 0), u16s(1),
			make([]byte, 32), u16s(0x18, 0xFFFF), avcC(sps, pps),
		}, nil)
		stbl := box("stbl",
			box("stsd", u32s(0, 1), box("avc1", avc1)),
			box("stts", u32s(0, 1, uint32(len(frames)), delta)),
			box("stss", stss),
			box("stsc", u32s(0, 1, 1, uint32(len(frames)), 1)),
			box("stsz", stsz),
			box("stco", u32s(0, 1, offset)),
		)
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
		trak := box("trak",
			box("tkhd", u32s(3, 0, 0, 1, 0, dur, 0, 0), u16s(0, 0, 0, 0), matrix, u32s(videoWidth<<16, videoHeight<<16)),
			mdia,
		)
		mvhd := box("mvhd", u32s(0, 0, 0, timescale, dur, 0x10000), u16s(0x100, 0), make([]byte, 8), matrix, make([]byte, 24), u32s(2))
		return box("moov", mvhd, trak)
	}
	head := uint32(len(ftyp) + len(moov(0)) + 8)
	return bytes.Join([][]byte{ftyp, moov(head), box("mdat", mdat.Bytes())}, nil), nil
}
