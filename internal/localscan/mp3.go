package localscan

import (
	"encoding/binary"
	"io"
	"os"
)

// mp3VBR reports whether an MP3 file has a variable bitrate, from the header
// in its first frame: "Xing" or "VBRI" is VBR, "Info" (LAME's CBR header) or
// no header is CBR. audioBytes is the byte count of the audio from the
// header, or 0 when the header has none.
func mp3VBR(path string) (vbr bool, audioBytes int64) {
	f, err := os.Open(path) //nolint:gosec // the user's music folder
	if err != nil {
		return false, 0
	}
	defer func() { _ = f.Close() }()

	// Skip an ID3v2 tag: "ID3", version, flags, and a syncsafe size.
	var head [10]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false, 0
	}
	var off int64
	if string(head[:3]) == "ID3" {
		size := int64(head[6]&0x7f)<<21 | int64(head[7]&0x7f)<<14 | int64(head[8]&0x7f)<<7 | int64(head[9]&0x7f)
		off = 10 + size
		if head[5]&0x10 != 0 {
			off += 10 // a footer
		}
	}
	buf := make([]byte, 64<<10)
	n, _ := f.ReadAt(buf, off)
	buf = buf[:n]

	// The first frame header: 11 set sync bits.
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xff || buf[i+1]&0xe0 != 0xe0 {
			continue
		}
		version := (buf[i+1] >> 3) & 3 // 3 = MPEG-1
		layer := (buf[i+1] >> 1) & 3   // 1 = layer III
		if version == 1 || layer != 1 || buf[i+2]>>4 == 15 || buf[i+2]>>4 == 0 || (buf[i+2]>>2)&3 == 3 {
			continue // not a valid layer III header
		}
		mono := buf[i+3]>>6 == 3
		side := 17 // MPEG-1 stereo: 32; MPEG-1 mono or MPEG-2 stereo: 17; MPEG-2 mono: 9
		switch {
		case version == 3 && !mono:
			side = 32
		case version != 3 && mono:
			side = 9
		}
		x := i + 4 + side
		if x+16 <= len(buf) {
			switch string(buf[x : x+4]) {
			case "Xing":
				flags := binary.BigEndian.Uint32(buf[x+4:])
				p := x + 8
				if flags&1 != 0 { // frame count
					p += 4
				}
				if flags&2 != 0 && p+4 <= len(buf) { // byte count
					audioBytes = int64(binary.BigEndian.Uint32(buf[p:]))
				}
				return true, audioBytes
			case "Info":
				return false, 0
			}
		}
		if v := i + 4 + 32; v+14 <= len(buf) && string(buf[v:v+4]) == "VBRI" {
			return true, int64(binary.BigEndian.Uint32(buf[v+10:]))
		}
		return false, 0
	}
	return false, 0
}
