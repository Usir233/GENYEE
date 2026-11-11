package tools

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Usir233/GENYEE/internal/infrastructure/legacyxls"
	"github.com/xuri/excelize/v2"
)

var oleCompoundHeader = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// prepareExcelForDuckDB returns a path DuckDB's read_xlsx can open.
// A legacy OLE workbook is converted to a temporary .xlsx; the cleanup
// function removes that file. Anything else, including a .xls that is
// really a renamed .xlsx, is returned unchanged.
func prepareExcelForDuckDB(filename string) (string, func(), error) {
	noop := func() {}
	ole, err := isOLECompoundFile(filename)
	if err != nil {
		return "", noop, err
	}
	if !ole {
		return filename, noop, nil
	}
	converted, err := convertLegacyXLSToXLSX(filename)
	if err != nil {
		return "", noop, err
	}
	cleanup := func() {
		_ = os.Remove(converted)
	}
	return converted, cleanup, nil
}

func isOLECompoundFile(filename string) (bool, error) {
	f, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, len(oleCompoundHeader))
	if _, err := io.ReadFull(f, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(header, oleCompoundHeader), nil
}

func convertLegacyXLSToXLSX(filename string) (string, error) {
	workbook, err := legacyxls.Open(filename)
	if err != nil {
		return "", fmt.Errorf("read xls workbook: %w", err)
	}
	if len(workbook.Sheets) == 0 {
		return "", fmt.Errorf("xls workbook %s has no sheets", filename)
	}
	out := excelize.NewFile()
	defer func() { _ = out.Close() }()

	// Create every sheet first: a stream writer must be flushed before the
	// workbook structure changes again.
	used := map[string]struct{}{}
	names := make([]string, len(workbook.Sheets))
	for i, sheet := range workbook.Sheets {
		names[i] = uniqueExcelSheetName(sheet.Name, i, used)
		if i == 0 {
			if err := out.SetSheetName("Sheet1", names[i]); err != nil {
				return "", err
			}
		} else if _, err := out.NewSheet(names[i]); err != nil {
			return "", err
		}
	}
	for i, sheet := range workbook.Sheets {
		if err := writeSheetCells(out, names[i], sheet.Cells); err != nil {
			return "", err
		}
	}

	tmp, err := os.CreateTemp("", "genyee-xls-*.xlsx")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := out.SaveAs(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

// writeSheetCells streams cells, sorted by row then column, into sheet as
// text. Streaming keeps large workbooks fast and their memory use flat.
func writeSheetCells(out *excelize.File, sheet string, cells []legacyxls.Cell) error {
	sw, err := out.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	var values []interface{}
	for start := 0; start < len(cells); {
		row, first := cells[start].Row, cells[start].Col
		end := start
		for end < len(cells) && cells[end].Row == row {
			end++
		}
		values = values[:0]
		for _, c := range cells[start:end] {
			for first+len(values) < c.Col {
				values = append(values, nil)
			}
			values = append(values, c.Value)
		}
		cell, err := excelize.CoordinatesToCellName(first+1, row+1)
		if err != nil {
			return err
		}
		if err := sw.SetRow(cell, values); err != nil {
			return err
		}
		start = end
	}
	return sw.Flush()
}

func uniqueExcelSheetName(name string, index int, used map[string]struct{}) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case ':', '\\', '/', '?', '*', '[', ']':
			return '_'
		default:
			return r
		}
	}, strings.TrimSpace(name))
	if name == "" {
		name = fmt.Sprintf("Sheet%d", index+1)
	}
	name = trimRunes(name, 31)
	base := name
	for n := 2; ; n++ {
		if _, ok := used[name]; !ok {
			used[name] = struct{}{}
			return name
		}
		suffix := fmt.Sprintf("_%d", n)
		name = trimRunes(base, 31-utf8.RuneCountInString(suffix)) + suffix
	}
}

func trimRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
