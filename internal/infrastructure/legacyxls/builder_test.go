package legacyxls

import (
	"bytes"
	"encoding/binary"
	"math"
	"unicode/utf16"
)

// This file builds BIFF streams and OLE2 compound files for tests, so edge
// cases (CONTINUE splits, BIFF5 code pages, encryption, malformed records)
// can be exercised without binary fixtures that no available tool can write.

type biffBuilder struct {
	buf bytes.Buffer
}

func (b *biffBuilder) rec(id uint16, parts ...[]byte) {
	var data []byte
	for _, p := range parts {
		data = append(data, p...)
	}
	b.raw(id, data)
}

func (b *biffBuilder) raw(id uint16, data []byte) {
	var h [4]byte
	binary.LittleEndian.PutUint16(h[:], id)
	binary.LittleEndian.PutUint16(h[2:], uint16(len(data)))
	b.buf.Write(h[:])
	b.buf.Write(data)
}

func u8(v byte) []byte { return []byte{v} }

func u16(v uint16) []byte {
	return binary.LittleEndian.AppendUint16(nil, v)
}

func u32(v uint32) []byte {
	return binary.LittleEndian.AppendUint32(nil, v)
}

func f64(v float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v))
}

func cell(row, col uint16) []byte {
	return append(append(u16(row), u16(col)...), u16(15)...) // xf 15
}

// chars8 encodes s as BIFF8 characters: compressed when every code point fits
// in a byte, UTF-16LE otherwise. It returns the option flag and the bytes.
func chars8(s string) (byte, []byte, int) {
	units := utf16.Encode([]rune(s))
	compressed := true
	for _, u := range units {
		if u > 0xFF {
			compressed = false
			break
		}
	}
	var out []byte
	if compressed {
		for _, u := range units {
			out = append(out, byte(u))
		}
		return 0, out, len(units)
	}
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return 1, out, len(units)
}

// xlString is a BIFF8 XLUnicodeString (16-bit length).
func xlString(s string) []byte {
	flag, data, n := chars8(s)
	return append(append(u16(uint16(n)), flag), data...)
}

// shortXLString is a BIFF8 ShortXLUnicodeString (8-bit length).
func shortXLString(s string) []byte {
	flag, data, n := chars8(s)
	return append([]byte{byte(n), flag}, data...)
}

// rkInt encodes an integer RK value; div100 marks it as scaled by 100.
func rkInt(v int32, div100 bool) uint32 {
	rk := uint32(v<<2) | 0x02
	if div100 {
		rk |= 0x01
	}
	return rk
}

// rkFloat encodes a double whose low 34 bits are zero as an RK value.
func rkFloat(v float64, div100 bool) uint32 {
	rk := uint32(math.Float64bits(v) >> 32 &^ 0x03)
	if div100 {
		rk |= 0x01
	}
	return rk
}

type testSheet struct {
	name    string
	kind    byte // BoundSheet8 dt: 0 worksheet, 2 chart
	records func(b *biffBuilder)
}

type workbookSpec struct {
	version uint16 // biff8 or biff5
	globals func(b *biffBuilder)
	sheets  []testSheet
	// encodeName encodes a BIFF5 sheet name; nil means raw bytes of the string.
	encodeName func(string) []byte
}

func bofData(version, kind uint16) []byte {
	if version == biff5 {
		return append(append(u16(version), u16(kind)...), make([]byte, 4)...)
	}
	return append(append(u16(version), u16(kind)...), make([]byte, 12)...)
}

// buildStream returns a complete workbook stream.
func buildStream(spec workbookSpec) []byte {
	if spec.version == 0 {
		spec.version = biff8
	}
	var b biffBuilder
	b.rec(recBOF, bofData(spec.version, 0x0005))
	if spec.globals != nil {
		spec.globals(&b)
	}
	patch := make([]int, len(spec.sheets))
	for i, s := range spec.sheets {
		patch[i] = b.buf.Len() + 4
		var name []byte
		if spec.version == biff8 {
			name = shortXLString(s.name)
		} else {
			raw := []byte(s.name)
			if spec.encodeName != nil {
				raw = spec.encodeName(s.name)
			}
			name = append([]byte{byte(len(raw))}, raw...)
		}
		b.rec(recBoundSheet, u32(0), u8(0), u8(s.kind), name)
	}
	b.rec(recEOF)
	out := b.buf.Bytes()
	for i, s := range spec.sheets {
		var sb biffBuilder
		sb.rec(recBOF, bofData(spec.version, 0x0010))
		if s.records != nil {
			s.records(&sb)
		}
		sb.rec(recEOF)
		binary.LittleEndian.PutUint32(out[patch[i]:], uint32(len(out)))
		out = append(out, sb.buf.Bytes()...)
	}
	return out
}

// buildCompoundFile wraps stream in a minimal version 3 OLE2 compound file
// with a single root-level stream. The stream is padded to 4096 bytes so it
// lives in regular sectors instead of the mini stream.
func buildCompoundFile(name string, stream []byte) []byte {
	const (
		sectorSize = 512
		freeSect   = 0xFFFFFFFF
		endOfChain = 0xFFFFFFFE
		fatSect    = 0xFFFFFFFD
		noStream   = 0xFFFFFFFF
	)
	// Streams shorter than the 4096-byte cutoff belong in the mini stream, so
	// pad the stream itself; readers stop at the final EOF record anyway.
	padded := append([]byte(nil), stream...)
	if len(padded) < 4096 {
		padded = append(padded, make([]byte, 4096-len(padded))...)
	}
	size := len(padded)
	if r := len(padded) % sectorSize; r != 0 {
		padded = append(padded, make([]byte, sectorSize-r)...)
	}
	dataSectors := len(padded) / sectorSize
	fatSectors := 1
	for (dataSectors+1+fatSectors)*4 > fatSectors*sectorSize {
		fatSectors++
	}
	if fatSectors > 109 {
		panic("test compound file too large")
	}
	dirSector := dataSectors
	firstFAT := dataSectors + 1

	header := make([]byte, sectorSize)
	copy(header, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	binary.LittleEndian.PutUint16(header[24:], 0x003E)
	binary.LittleEndian.PutUint16(header[26:], 3)
	binary.LittleEndian.PutUint16(header[28:], 0xFFFE)
	binary.LittleEndian.PutUint16(header[30:], 9)
	binary.LittleEndian.PutUint16(header[32:], 6)
	binary.LittleEndian.PutUint32(header[44:], uint32(fatSectors))
	binary.LittleEndian.PutUint32(header[48:], uint32(dirSector))
	binary.LittleEndian.PutUint32(header[56:], 4096)
	binary.LittleEndian.PutUint32(header[60:], endOfChain)
	binary.LittleEndian.PutUint32(header[68:], endOfChain)
	for i := 0; i < 109; i++ {
		v := uint32(freeSect)
		if i < fatSectors {
			v = uint32(firstFAT + i)
		}
		binary.LittleEndian.PutUint32(header[76+4*i:], v)
	}

	dirEntry := func(name string, kind byte, child uint32, start uint32, size uint32) []byte {
		e := make([]byte, 128)
		units := utf16.Encode([]rune(name))
		for i, u := range units {
			binary.LittleEndian.PutUint16(e[2*i:], u)
		}
		binary.LittleEndian.PutUint16(e[64:], uint16(2*(len(units)+1)))
		e[66] = kind
		e[67] = 1 // black
		binary.LittleEndian.PutUint32(e[68:], noStream)
		binary.LittleEndian.PutUint32(e[72:], noStream)
		binary.LittleEndian.PutUint32(e[76:], child)
		binary.LittleEndian.PutUint32(e[116:], start)
		binary.LittleEndian.PutUint32(e[120:], size)
		return e
	}
	dir := append(dirEntry("Root Entry", 5, 1, endOfChain, 0), dirEntry(name, 2, noStream, 0, uint32(size))...)
	for len(dir) < sectorSize {
		dir = append(dir, dirEntry("", 0, noStream, 0, 0)...)
	}

	fat := make([]byte, fatSectors*sectorSize)
	for i := range fatSectors * sectorSize / 4 {
		binary.LittleEndian.PutUint32(fat[4*i:], freeSect)
	}
	for i := 0; i < dataSectors; i++ {
		next := uint32(i + 1)
		if i == dataSectors-1 {
			next = endOfChain
		}
		binary.LittleEndian.PutUint32(fat[4*i:], next)
	}
	binary.LittleEndian.PutUint32(fat[4*dirSector:], endOfChain)
	for i := 0; i < fatSectors; i++ {
		binary.LittleEndian.PutUint32(fat[4*(firstFAT+i):], fatSect)
	}

	out := append([]byte(nil), header...)
	out = append(out, padded...)
	out = append(out, dir...)
	return append(out, fat...)
}

func buildWorkbookFile(spec workbookSpec) []byte {
	name := "Workbook"
	if spec.version == biff5 {
		name = "Book"
	}
	return buildCompoundFile(name, buildStream(spec))
}
