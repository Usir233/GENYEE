package legacyxls

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseStream feeds arbitrary workbook streams to the BIFF parser.
//
//	go test ./internal/infrastructure/legacyxls -run '^$' -fuzz FuzzParseStream -fuzztime 60s
func FuzzParseStream(f *testing.F) {
	f.Add(buildStream(workbookSpec{
		globals: func(b *biffBuilder) {
			data := append(u32(2), u32(2)...)
			data = append(data, xlString("hello")...)
			data = append(data, xlString("世界")...)
			b.raw(recSST, data)
			b.raw(recContinue, []byte{0x01, 'a', 0})
		},
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recLabelSST, cell(0, 0), u32(1))
			b.rec(recMulRK, u16(1), u16(0), u16(15), u32(rkInt(-1, true)), u16(0))
			b.rec(recFormula, cell(2, 0), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
			b.rec(recString, xlString("x"))
			b.rec(recBOF, bofData(biff8, 0x20))
			b.rec(recEOF)
		}}},
	}))
	f.Add(buildStream(workbookSpec{
		version: biff5,
		globals: func(b *biffBuilder) { b.rec(recCodePage, u16(936)) },
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recLabel, cell(0, 0), u16(2), []byte{0xC3, 0xFB})
		}}},
	}))
	f.Fuzz(func(t *testing.T, stream []byte) {
		wb, err := parseStream(stream)
		if err == nil && wb == nil {
			t.Fatal("nil workbook without error")
		}
	})
}

// FuzzRead fuzzes the whole path, compound file container included.
func FuzzRead(f *testing.F) {
	for _, name := range []string{"legacy.xls", "edge.xls"} {
		if data, err := os.ReadFile(filepath.Join("..", "..", "agent", "tools", "testdata", name)); err == nil {
			f.Add(data)
		}
	}
	f.Add(buildWorkbookFile(workbookSpec{sheets: []testSheet{{name: "S"}}}))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Read(bytes.NewReader(data), int64(len(data)))
	})
}
