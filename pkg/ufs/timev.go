package ufs

import "time"

// TimeNow returns a TimeV set to the current time in V2 format
// (64-bit milliseconds since Unix epoch).
func TimeNow() TimeV {
	return TimeFromGo(time.Now())
}

// TimeFromGo converts a Go time.Time to a TimeV (V2 format).
func TimeFromGo(t time.Time) TimeV {
	ms := uint64(t.UnixMilli())
	var tv TimeV
	putU64BE(tv.Raw[:], ms)
	return tv
}

// ToGo converts a TimeV to a Go time.Time.
// Automatically detects V1 vs V2 format.
func (tv TimeV) ToGo() time.Time {
	hi := getU32BE(tv.Raw[0:4])
	lo := getU32BE(tv.Raw[4:8])

	if lo < 1_000_000 {
		// V1: hi = seconds, lo = microseconds
		return time.Unix(int64(hi), int64(lo)*1000)
	}
	// V2: full 8 bytes = milliseconds since epoch
	ms := getU64BE(tv.Raw[:])
	return time.UnixMilli(int64(ms))
}

// IsZero returns true if the timestamp is all zeros.
func (tv TimeV) IsZero() bool {
	for _, b := range tv.Raw {
		if b != 0 {
			return false
		}
	}
	return true
}

// IsV1 returns true if this looks like a V1 timestamp.
func (tv TimeV) IsV1() bool {
	lo := getU32BE(tv.Raw[4:8])
	return lo < 1_000_000
}

// helpers for big-endian byte manipulation
func getU16BE(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}

func putU16BE(b []byte, v uint16) {
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}

func getU32BE(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func putU32BE(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

func getU64BE(b []byte) uint64 {
	return uint64(getU32BE(b[0:4]))<<32 | uint64(getU32BE(b[4:8]))
}

func putU64BE(b []byte, v uint64) {
	putU32BE(b[0:4], uint32(v>>32))
	putU32BE(b[4:8], uint32(v))
}
