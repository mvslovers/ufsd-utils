package ebcdic

import (
	"testing"
)

func TestDefaultCodepageIsIBM1047(t *testing.T) {
	if ActiveCodepage() != IBM1047 {
		t.Errorf("default codepage = %v, want IBM1047", ActiveCodepage())
	}
}

func TestSetCodepage(t *testing.T) {
	// Save and restore
	orig := ActiveCodepage()
	defer SetCodepage(orig)

	if err := SetCodepage(CP037); err != nil {
		t.Fatal(err)
	}
	if ActiveCodepage() != CP037 {
		t.Errorf("after SetCodepage(CP037): got %v", ActiveCodepage())
	}

	if err := SetCodepage(IBM1047); err != nil {
		t.Fatal(err)
	}
	if ActiveCodepage() != IBM1047 {
		t.Errorf("after SetCodepage(IBM1047): got %v", ActiveCodepage())
	}

	if err := SetCodepage(Codepage(99)); err == nil {
		t.Error("SetCodepage(99) should return error")
	}
}

func TestCodepageString(t *testing.T) {
	if s := IBM1047.String(); s != "IBM1047" {
		t.Errorf("IBM1047.String() = %q", s)
	}
	if s := CP037.String(); s != "CP037" {
		t.Errorf("CP037.String() = %q", s)
	}
}

// TestIBM1047CriticalChars verifies the critical bracket/pipe positions
// that must match HTTPD's httpxlat.c for HTML/CSS/JS roundtrip.
func TestIBM1047CriticalChars(t *testing.T) {
	SetCodepage(IBM1047)
	defer SetCodepage(IBM1047)

	// From the issue acceptance criteria:
	// Encode("[]{|}\^~", 0) should produce 0xAD 0xBD 0xC0 0x4F 0xD0 0xE0 0x5F 0xA1
	input := "[]{|}\\^~"
	expect := []byte{0xAD, 0xBD, 0xC0, 0x4F, 0xD0, 0xE0, 0x5F, 0xA1}

	got := Encode(input, 0)
	if len(got) != len(expect) {
		t.Fatalf("Encode(%q, 0): len = %d, want %d", input, len(got), len(expect))
	}
	for i := range expect {
		if got[i] != expect[i] {
			t.Errorf("Encode(%q, 0)[%d] = 0x%02X, want 0x%02X", input, i, got[i], expect[i])
		}
	}
}

// TestCP037CaretDifference verifies the key difference between CP037 and IBM-1047.
func TestCP037CaretDifference(t *testing.T) {
	orig := ActiveCodepage()
	defer SetCodepage(orig)

	SetCodepage(CP037)
	got := Encode("^", 0)
	if got[0] != 0xB0 {
		t.Errorf("CP037 Encode('^') = 0x%02X, want 0xB0", got[0])
	}

	SetCodepage(IBM1047)
	got = Encode("^", 0)
	if got[0] != 0x5F {
		t.Errorf("IBM1047 Encode('^') = 0x%02X, want 0x5F", got[0])
	}
}

// TestPrintableASCIIRoundtrip checks that all 95 printable ASCII characters
// survive an Encode→Decode roundtrip.
func TestPrintableASCIIRoundtrip(t *testing.T) {
	for _, cp := range []Codepage{IBM1047, CP037} {
		t.Run(cp.String(), func(t *testing.T) {
			SetCodepage(cp)
			defer SetCodepage(IBM1047)

			for c := byte(0x21); c <= 0x7E; c++ { // skip 0x20 (space): Decode trims trailing spaces
				input := string([]byte{c})
				encoded := Encode(input, 0)
				decoded := Decode(encoded)
				if decoded != input {
					t.Errorf("char 0x%02X (%q): roundtrip got %q (EBCDIC=0x%02X)",
						c, input, decoded, encoded[0])
				}
			}
		})
	}
}

func TestEncodeWithPadding(t *testing.T) {
	got := Encode("AB", 8)
	if len(got) != 8 {
		t.Fatalf("Encode('AB', 8): len = %d, want 8", len(got))
	}
	// Padding should be EBCDIC space (0x40)
	for i := 2; i < 8; i++ {
		if got[i] != 0x40 {
			t.Errorf("Encode('AB', 8)[%d] = 0x%02X, want 0x40 (EBCDIC space)", i, got[i])
		}
	}
}

func TestEncodeTruncation(t *testing.T) {
	got := Encode("ABCDEF", 3)
	if len(got) != 3 {
		t.Fatalf("Encode('ABCDEF', 3): len = %d, want 3", len(got))
	}
}

func TestDecodeTrimsSpaceAndNul(t *testing.T) {
	// EBCDIC "A" (0xC1) followed by spaces and NUL
	input := []byte{0xC1, 0x40, 0x40, 0x00, 0x00}
	got := Decode(input)
	if got != "A" {
		t.Errorf("Decode with trailing space+NUL: got %q, want %q", got, "A")
	}
}

func TestEncodeBytesInPlace(t *testing.T) {
	SetCodepage(IBM1047)
	buf := []byte("Hello")
	EncodeBytes(buf)
	// 'H' in IBM-1047 = 0xC8
	if buf[0] != 0xC8 {
		t.Errorf("EncodeBytes: 'H' = 0x%02X, want 0xC8", buf[0])
	}
	// Decode back
	DecodeBytes(buf)
	if string(buf) != "Hello" {
		t.Errorf("DecodeBytes roundtrip: got %q, want %q", string(buf), "Hello")
	}
}

func TestEncodeWithExplicitTable(t *testing.T) {
	// Use CP037 table explicitly even when IBM1047 is active
	SetCodepage(IBM1047)
	defer SetCodepage(IBM1047)

	table := TableAtoE(CP037)
	got := EncodeWith("^", 0, table)
	if got[0] != 0xB0 {
		t.Errorf("EncodeWith(CP037, '^') = 0x%02X, want 0xB0", got[0])
	}
}

func TestDecodeWithExplicitTable(t *testing.T) {
	SetCodepage(IBM1047)
	defer SetCodepage(IBM1047)

	table := TableEtoA(CP037)
	got := DecodeWith([]byte{0xB0}, table)
	if got != "^" {
		t.Errorf("DecodeWith(CP037, 0xB0) = %q, want '^'", got)
	}
}
