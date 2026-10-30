package legacyxls

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func readBytes(t testing.TB, data []byte) (*Workbook, error) {
	t.Helper()
	return Read(bytes.NewReader(data), int64(len(data)))
}

// cells flattens a sheet into "A1"-style keys for readable assertions.
func cells(s Sheet) map[string]string {
	out := map[string]string{}
	for _, c := range s.Cells {
		out[cellName(c.Row, c.Col)] = c.Value
	}
	return out
}

func cellName(r, c int) string {
	col := ""
	for n := c + 1; n > 0; n = (n - 1) / 26 {
		col = string(rune('A'+(n-1)%26)) + col
	}
	return col + itoa(r+1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestDecodeRK(t *testing.T) {
	cases := []struct {
		rk   uint32
		want string
	}{
		{rkInt(3, false), "3"},
		{rkInt(-5, false), "-5"},
		{rkInt(-536870912, false), "-536870912"}, // smallest 30-bit integer
		{rkInt(536870911, false), "536870911"},
		{rkInt(1234, true), "12.34"},
		{rkInt(-1234, true), "-12.34"},
		{rkFloat(12.5, false), "12.5"},
		{rkFloat(-0.125, false), "-0.125"},
		{rkFloat(1250, true), "12.5"},
		{rkFloat(0, false), "0"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, formatNumber(decodeRK(tc.rk)), "rk=0x%08X", tc.rk)
	}
}

func TestFormatNumber(t *testing.T) {
	cases := map[float64]string{
		3:                  "3",
		12.5:               "12.5",
		-0.125:             "-0.125",
		1234567890123:      "1234567890123",
		1e-7:               "0.0000001",
		6.02e23:            "602000000000000000000000",
		1.0 / 3:            "0.3333333333333333",
		0:                  "0",
		45351.57298611111:  "45351.57298611111",
		-1234567.000000001: "-1234567.000000001",
	}
	for v, want := range cases {
		require.Equal(t, want, formatNumber(v))
	}
	negZero := 0.0
	negZero = -negZero
	require.Equal(t, "0", formatNumber(negZero))
}

func TestReadBIFF8CellTypes(t *testing.T) {
	sst := func(b *biffBuilder) {
		var data []byte
		strs := []string{"名称", "apple", "中文：苹果，梨🍐", "", "  pad  "}
		data = append(data, u32(uint32(len(strs)+3))...)
		data = append(data, u32(uint32(len(strs)))...)
		for _, s := range strs {
			data = append(data, xlString(s)...)
		}
		b.raw(recSST, data)
	}
	data := buildWorkbookFile(workbookSpec{
		globals: func(b *biffBuilder) {
			b.rec(recCodePage, u16(1200))
			sst(b)
		},
		sheets: []testSheet{
			{name: "销售", records: func(b *biffBuilder) {
				b.rec(recLabelSST, cell(0, 0), u32(0))
				b.rec(recLabelSST, cell(1, 0), u32(1))
				b.rec(recLabelSST, cell(2, 0), u32(2))
				b.rec(recLabelSST, cell(3, 0), u32(3))  // empty string: no cell
				b.rec(recLabelSST, cell(4, 0), u32(4))  // whitespace kept
				b.rec(recLabelSST, cell(5, 0), u32(99)) // out of range: ignored
				b.rec(recNumber, cell(0, 1), f64(12.5))
				b.rec(recRK, cell(1, 1), u32(rkInt(-7, false)))
				b.rec(recRK, cell(2, 1), u32(rkFloat(-0.125, false)))
				// MULRK: row 6, columns 1..3.
				b.rec(recMulRK, u16(6), u16(1),
					u16(15), u32(rkInt(1, false)),
					u16(15), u32(rkInt(-2, false)),
					u16(15), u32(rkInt(305, true)),
					u16(3))
				b.rec(recBoolErr, cell(7, 0), u8(1), u8(0))
				b.rec(recBoolErr, cell(7, 1), u8(0), u8(0))
				b.rec(recBoolErr, cell(7, 2), u8(0x07), u8(1))
				b.rec(recBoolErr, cell(7, 3), u8(0x2A), u8(1))
				b.rec(recLabel, cell(8, 0), xlString("label ü"))
				b.rec(recRString, cell(8, 1), xlString("rich"), u16(1), u16(0), u16(1))
				// Formulas: numeric, string (+STRING), boolean, error, empty string.
				b.rec(recFormula, cell(9, 0), f64(6), u16(0), u32(0), u16(0))
				b.rec(recFormula, cell(9, 1), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
				b.rec(0x04BC /* SHRFMLA may sit in between */, make([]byte, 8))
				b.rec(recString, xlString("abcd"))
				b.rec(recFormula, cell(9, 2), []byte{1, 0, 1, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
				b.rec(recFormula, cell(9, 3), []byte{2, 0, 0x17, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
				b.rec(recFormula, cell(9, 4), []byte{3, 0, 0, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
				// A stray STRING without a pending formula is ignored.
				b.rec(recString, xlString("stray"))
				// An embedded chart substream must not leak cells.
				b.rec(recBOF, bofData(biff8, 0x0020))
				b.rec(recNumber, cell(0, 0), f64(999))
				b.rec(recEOF)
				b.rec(recNumber, cell(30, 5), f64(1))
				// Later records overwrite earlier ones at the same cell.
				b.rec(recNumber, cell(30, 5), f64(2))
			}},
			{name: "Chart1", kind: 2},
			{name: "空表"},
			{name: "a/b:c"},
		},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Len(t, wb.Sheets, 3, "chart sheets are skipped")
	require.Equal(t, "销售", wb.Sheets[0].Name)
	require.Equal(t, "空表", wb.Sheets[1].Name)
	require.Equal(t, "a/b:c", wb.Sheets[2].Name)
	require.Empty(t, cells(wb.Sheets[1]))

	require.Equal(t, map[string]string{
		"A1": "名称", "B1": "12.5",
		"A2": "apple", "B2": "-7",
		"A3": "中文：苹果，梨🍐", "B3": "-0.125",
		"A5": "  pad  ",
		"B7": "1", "C7": "-2", "D7": "3.05",
		"A8": "TRUE", "B8": "FALSE", "C8": "#DIV/0!", "D8": "#N/A",
		"A9": "label ü", "B9": "rich",
		"A10": "6", "B10": "abcd", "C10": "TRUE", "D10": "#REF!",
		"F31": "2",
	}, cells(wb.Sheets[0]))
}

// sstSegments builds an SST whose string data is split into the given
// CONTINUE chunks by the caller-supplied layout function.
func TestSSTCharactersSplitAcrossContinue(t *testing.T) {
	long := strings.Repeat("ab", 20) // compressed
	wide := strings.Repeat("中文", 10) // UTF-16

	// SST header + string 1 header, then 10 compressed chars in the SST.
	sstData := append(u32(3), u32(3)...)
	sstData = append(append(sstData, u16(uint16(len(long)))...), 0x00)
	sstData = append(sstData, long[:10]...)
	// CONTINUE 1: option byte switches to UTF-16 for the remaining 30 chars.
	var cont1 []byte
	cont1 = append(cont1, 0x01)
	for _, r := range long[10:] {
		cont1 = append(cont1, byte(r), 0)
	}
	// Second string: rich text with 1 run; its header ends the record.
	cont1 = append(append(cont1, u16(uint16(len([]rune(wide))))...), 0x09)
	cont1 = append(cont1, u16(1)...)
	// CONTINUE 2: a split character array always restarts with an option byte.
	cont2 := []byte{0x01}
	for _, r := range wide {
		cont2 = append(cont2, byte(r), byte(r>>8))
	}
	// The run (4 bytes) is split across records without an option byte.
	cont2 = append(cont2, 0, 0)
	cont3 := []byte{1, 0}
	// Third string with an ExtRst tail of 6 bytes.
	cont3 = append(append(cont3, u16(3)...), 0x04)
	cont3 = append(cont3, u32(6)...)
	cont3 = append(cont3, "end"...)
	cont3 = append(cont3, make([]byte, 6)...)

	data := buildWorkbookFile(workbookSpec{
		globals: func(b *biffBuilder) {
			b.raw(recSST, sstData)
			b.raw(recContinue, cont1)
			b.raw(recContinue, cont2)
			b.raw(recContinue, cont3)
		},
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recLabelSST, cell(0, 0), u32(0))
			b.rec(recLabelSST, cell(1, 0), u32(1))
			b.rec(recLabelSST, cell(2, 0), u32(2))
		}}},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"A1": long, "A2": wide, "A3": "end"}, cells(wb.Sheets[0]))
}

func TestSSTWideToCompressedSwitch(t *testing.T) {
	sstData := append(u32(1), u32(1)...)
	sstData = append(append(sstData, u16(4)...), 0x01)
	sstData = append(sstData, 0x2D, 0x4E, 0x87, 0x65) // 中文
	cont := []byte{0x00, 'o', 'k'}
	data := buildWorkbookFile(workbookSpec{
		globals: func(b *biffBuilder) {
			b.raw(recSST, sstData)
			b.raw(recContinue, cont)
		},
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recLabelSST, cell(0, 0), u32(0))
		}}},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Equal(t, "中文ok", cells(wb.Sheets[0])["A1"])
}

func TestReadBIFF5CodePage(t *testing.T) {
	gbk := func(s string) []byte {
		b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		require.NoError(t, err)
		return b
	}
	label := func(s string) []byte {
		raw := gbk(s)
		return append(u16(uint16(len(raw))), raw...)
	}
	data := buildWorkbookFile(workbookSpec{
		version:    biff5,
		encodeName: func(s string) []byte { return gbk(s) },
		globals: func(b *biffBuilder) {
			b.rec(recCodePage, u16(936))
		},
		sheets: []testSheet{{name: "销售", records: func(b *biffBuilder) {
			b.rec(recLabel, cell(0, 0), label("名称"))
			b.rec(recLabel, cell(0, 1), label("数量"))
			b.rec(recLabel, cell(1, 0), label("苹果"))
			b.rec(recRK, cell(1, 1), u32(rkInt(-3, false)))
			b.rec(recNumber, cell(2, 1), f64(12.5))
			b.rec(recFormula, cell(3, 0), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
			b.rec(recString, label("公式"))
		}}},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Len(t, wb.Sheets, 1)
	require.Equal(t, "销售", wb.Sheets[0].Name)
	require.Equal(t, map[string]string{
		"A1": "名称", "B1": "数量",
		"A2": "苹果", "B2": "-3",
		"B3": "12.5", "A4": "公式",
	}, cells(wb.Sheets[0]))
}

func TestReadBIFF5DefaultCodePageIsWindows1252(t *testing.T) {
	data := buildWorkbookFile(workbookSpec{
		version: biff5,
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recLabel, cell(0, 0), u16(4), []byte{'c', 'a', 'f', 0xE9})
			b.rec(recLabel, cell(0, 1), u16(1), []byte{0x80})
		}}},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"A1": "café", "B1": "€"}, cells(wb.Sheets[0]))
}

func TestBookAndWorkbookStreamsPreferBIFF8(t *testing.T) {
	// A file carrying only a "Book" stream is read as BIFF5.
	data := buildCompoundFile("Book", buildStream(workbookSpec{
		version: biff5,
		sheets:  []testSheet{{name: "Old"}},
	}))
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Equal(t, "Old", wb.Sheets[0].Name)
}

func TestEncryptedWorkbook(t *testing.T) {
	data := buildWorkbookFile(workbookSpec{
		globals: func(b *biffBuilder) {
			b.rec(recFilePass, u16(1), make([]byte, 52))
		},
		sheets: []testSheet{{name: "S"}},
	})
	_, err := readBytes(t, data)
	require.ErrorIs(t, err, ErrEncrypted)
}

func TestNotAWorkbook(t *testing.T) {
	_, err := readBytes(t, buildCompoundFile("WordDocument", []byte("not excel")))
	require.ErrorIs(t, err, ErrUnsupported)

	_, err = readBytes(t, buildCompoundFile("Workbook", []byte("garbage!")))
	require.ErrorIs(t, err, ErrUnsupported)

	// BIFF2-4 BOF versions are not supported.
	var b biffBuilder
	b.rec(recBOF, u16(0x0400), u16(0x0005), make([]byte, 4))
	_, err = readBytes(t, buildCompoundFile("Workbook", b.buf.Bytes()))
	require.ErrorIs(t, err, ErrUnsupported)

	_, err = readBytes(t, []byte("PK\x03\x04 a zip file, not OLE2"))
	require.Error(t, err)
}

func TestTruncatedWorkbooksNeverPanic(t *testing.T) {
	full := buildStream(workbookSpec{
		globals: func(b *biffBuilder) {
			data := append(u32(2), u32(2)...)
			data = append(data, xlString("hello")...)
			data = append(data, xlString("世界")...)
			b.raw(recSST, data)
		},
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recLabelSST, cell(0, 0), u32(0))
			b.rec(recLabelSST, cell(0, 1), u32(1))
			b.rec(recMulRK, u16(1), u16(0), u16(15), u32(rkInt(1, false)), u16(15), u32(rkInt(2, false)), u16(1))
			b.rec(recFormula, cell(2, 0), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, u16(0), u32(0), u16(0))
			b.rec(recString, xlString("x"))
		}}},
	})
	for n := 0; n <= len(full); n++ {
		wb, err := parseStream(full[:n])
		if err == nil {
			require.NotNil(t, wb)
		}
	}
	// Corrupt every byte of the stream in turn.
	for i := range full {
		mutated := append([]byte(nil), full...)
		mutated[i] ^= 0xFF
		_, _ = parseStream(mutated)
	}
}

func TestMalformedRecordsAreSkipped(t *testing.T) {
	data := buildWorkbookFile(workbookSpec{
		globals: func(b *biffBuilder) {
			// SST claiming four billion strings with no data.
			b.rec(recSST, u32(0xFFFFFFFF), u32(0xFFFFFFFF))
			b.rec(recBoundSheet, u32(0xFFFFFFF0), u8(0), u8(0), shortXLString("dangling"))
		},
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recNumber, cell(0, 0)) // too short
			b.rec(recRK, u16(1))         // too short
			b.rec(recMulRK, u16(0))      // too short
			b.rec(recLabel, cell(0, 1), u16(500), u8(0), []byte("short"))
			b.rec(recLabelSST, cell(0, 2), u32(0))
			b.rec(recNumber, cell(0xFFFF, 0xFFFF), f64(1)) // beyond the column limit
			b.rec(recNumber, cell(3, 3), f64(4))
		}}},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Len(t, wb.Sheets, 2)
	require.Equal(t, "dangling", wb.Sheets[0].Name)
	require.Empty(t, wb.Sheets[0].Cells)
	require.Equal(t, map[string]string{"B1": "short", "D4": "4"}, cells(wb.Sheets[1]))
}

func TestOpenRealFixtures(t *testing.T) {
	wb, err := Open(filepath.Join("..", "..", "agent", "tools", "testdata", "legacy.xls"))
	require.NoError(t, err)
	require.Len(t, wb.Sheets, 2)
	require.Equal(t, "销售", wb.Sheets[0].Name)
	require.Equal(t, map[string]string{
		"A1": "名称", "B1": "数量", "A2": "苹果", "B2": "3", "A3": "梨", "B3": "12.5",
	}, cells(wb.Sheets[0]))
	require.Equal(t, "库存", wb.Sheets[1].Name)
	require.Equal(t, map[string]string{"A1": "sku", "A2": "A-1"}, cells(wb.Sheets[1]))

	wb, err = Open(filepath.Join("..", "..", "agent", "tools", "testdata", "edge.xls"))
	require.NoError(t, err)
	require.Equal(t, []string{"边界 Edge", "空表", "Sheet1"}, sheetNames(wb))
	got := cells(wb.Sheets[0])
	want := map[string]string{
		"A1": "int", "B1": "3",
		"A2": "float", "B2": "12.5",
		"A3": "neg", "B3": "-0.125",
		"A4": "big", "B4": "1234567890123",
		"A5": "tiny", "B5": "0.0000001",
		"A6": "sci", "B6": "602000000000000000000000",
		"A7": "bool_t", "B7": "TRUE",
		"A8": "bool_f", "B8": "FALSE",
		"A9":  "empty_str",
		"A10": "cjk", "B10": "中文：苹果，梨🍐",
		"A11": "long", "B11": strings.Repeat("x", 9000),
		"A12": "newline", "B12": "a\nb",
		"A13": "spaces", "B13": "  pad  ",
		"A14": "zero", "B14": "0",
		"A15": "third", "B15": "0.3333333333333333",
		"A21": "date", "B21": "45351",
		"A22": "datetime", "B22": "45351.57298611111",
		"A23": "percent", "B23": "0.256",
		"A24": "formula",
		"A25": "formula_str",
		"A27": "merged",
		"F31": "sparse far cell",
	}
	require.Equal(t, want, got)
	require.Empty(t, cells(wb.Sheets[1]))
	require.Equal(t, map[string]string{"A1": "reserved chars"}, cells(wb.Sheets[2]))
}

func TestOpenMissingFile(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing.xls"))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrUnsupported))
}

func sheetNames(wb *Workbook) []string {
	names := make([]string, len(wb.Sheets))
	for i, s := range wb.Sheets {
		names[i] = s.Name
	}
	return names
}
