// Package ebcdic provides conversion between EBCDIC (Code Page 037)
// and ASCII/UTF-8. CP037 is the standard US/Canada EBCDIC variant
// used on MVS systems.
package ebcdic

// cp037ToASCII maps EBCDIC CP037 bytes to ASCII.
var cp037ToASCII [256]byte

// asciiToCP037 maps ASCII bytes to EBCDIC CP037.
var asciiToCP037 [256]byte

func init() {
	// Initialize with substitution character
	for i := range cp037ToASCII {
		cp037ToASCII[i] = 0x1A // ASCII SUB
	}
	for i := range asciiToCP037 {
		asciiToCP037[i] = 0x6F // EBCDIC '?'
	}

	// CP037 -> ASCII mapping (standard pairs)
	pairs := [256]byte{
		0x00: 0x00, 0x01: 0x01, 0x02: 0x02, 0x03: 0x03,
		0x05: 0x09, 0x0B: 0x0B, 0x0C: 0x0C, 0x0D: 0x0D,
		0x0E: 0x0E, 0x0F: 0x0F, 0x10: 0x10, 0x11: 0x11,
		0x12: 0x12, 0x13: 0x13, 0x15: 0x0A, 0x25: 0x0A,
		0x40: 0x20, // space
		0x4B: 0x2E, // .
		0x4C: 0x3C, // <
		0x4D: 0x28, // (
		0x4E: 0x2B, // +
		0x4F: 0x7C, // |
		0x50: 0x26, // &
		0x5A: 0x21, // !
		0x5B: 0x24, // $
		0x5C: 0x2A, // *
		0x5D: 0x29, // )
		0x5E: 0x3B, // ;
		0x5F: 0x5E, // ^
		0x60: 0x2D, // -
		0x61: 0x2F, // /
		0x6A: 0x7E, // ~  (CP037 specific)
		0x6B: 0x2C, // ,
		0x6C: 0x25, // %
		0x6D: 0x5F, // _
		0x6E: 0x3E, // >
		0x6F: 0x3F, // ?
		0x7A: 0x3A, // :
		0x7B: 0x23, // #
		0x7C: 0x40, // @
		0x7D: 0x27, // '
		0x7E: 0x3D, // =
		0x7F: 0x22, // "
		0xAD: 0x5B, // [  (CP037 specific)
		0xBA: 0x5D, // ]  (CP037 specific)
		0xBD: 0x5D, // ]  (alternate)
		0xC0: 0x7B, // {
		0xD0: 0x7D, // }
		0xE0: 0x5C, // backslash
		0xE2: 0x53, // S (placeholder, overwritten below)
	}

	// Lowercase a-z: EBCDIC 0x81-0x89 (a-i), 0x91-0x99 (j-r), 0xA2-0xA9 (s-z)
	for i := byte(0); i <= 8; i++ {
		pairs[0x81+i] = 'a' + i       // a-i
		pairs[0x91+i] = 'j' + i       // j-r
	}
	for i := byte(0); i <= 7; i++ {
		pairs[0xA2+i] = 's' + i       // s-z
	}

	// Uppercase A-Z: EBCDIC 0xC1-0xC9 (A-I), 0xD1-0xD9 (J-R), 0xE2-0xE9 (S-Z)
	for i := byte(0); i <= 8; i++ {
		pairs[0xC1+i] = 'A' + i       // A-I
		pairs[0xD1+i] = 'J' + i       // J-R
	}
	for i := byte(0); i <= 7; i++ {
		pairs[0xE2+i] = 'S' + i       // S-Z
	}

	// Digits 0-9: EBCDIC 0xF0-0xF9
	for i := byte(0); i <= 9; i++ {
		pairs[0xF0+i] = '0' + i
	}

	// Apply forward mapping and build reverse
	for e := 0; e < 256; e++ {
		a := pairs[e]
		if a != 0 || e == 0 {
			cp037ToASCII[e] = a
			asciiToCP037[a] = byte(e)
		}
	}

	// Fix NL: EBCDIC 0x15 -> ASCII LF, but also 0x25 -> LF
	asciiToCP037[0x0A] = 0x15 // prefer NL for ASCII LF -> EBCDIC
}

// ToASCII converts an EBCDIC byte slice to ASCII in-place and returns it.
func ToASCII(buf []byte) []byte {
	for i, b := range buf {
		buf[i] = cp037ToASCII[b]
	}
	return buf
}

// ToEBCDIC converts an ASCII byte slice to EBCDIC in-place and returns it.
func ToEBCDIC(buf []byte) []byte {
	for i, b := range buf {
		buf[i] = asciiToCP037[b]
	}
	return buf
}

// DecodeString converts an EBCDIC byte slice to a Go (UTF-8) string.
// Stops at the first NUL byte.
func DecodeString(ebcdicBytes []byte) string {
	n := len(ebcdicBytes)
	for i, b := range ebcdicBytes {
		if b == 0 {
			n = i
			break
		}
	}
	ascii := make([]byte, n)
	for i := 0; i < n; i++ {
		ascii[i] = cp037ToASCII[ebcdicBytes[i]]
	}
	return string(ascii)
}

// EncodeString converts a Go (UTF-8/ASCII) string to an EBCDIC byte slice
// of the given length, NUL-padded.
func EncodeString(s string, length int) []byte {
	buf := make([]byte, length)
	for i := 0; i < len(s) && i < length-1; i++ {
		buf[i] = asciiToCP037[s[i]]
	}
	return buf
}
