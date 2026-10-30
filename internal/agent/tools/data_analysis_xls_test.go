package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestPrepareExcelForDuckDBConvertsLegacyXLS(t *testing.T) {
	src := filepath.Join("testdata", "legacy.xls")
	converted, cleanup, err := prepareExcelForDuckDB(src)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.NotEqual(t, src, converted)

	book, err := excelize.OpenFile(converted)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Close() })

	sales, err := book.GetRows("销售")
	require.NoError(t, err)
	require.Equal(t, []string{"名称", "数量"}, sales[0])
	require.Equal(t, []string{"苹果", "3"}, sales[1])
	require.Equal(t, []string{"梨", "12.5"}, sales[2])

	stock, err := book.GetRows("库存")
	require.NoError(t, err)
	require.Equal(t, [][]string{{"sku"}, {"A-1"}}, stock)
}

func TestPrepareExcelForDuckDBLeavesNonOLEXlsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.xls")
	require.NoError(t, os.WriteFile(path, []byte("PK\x03\x04not-an-xls"), 0o644))
	got, cleanup, err := prepareExcelForDuckDB(path)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, path, got)
}

func TestPrepareExcelForDuckDBAcceptsUppercaseExtension(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "legacy.xls"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "LEGACY.XLS")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	converted, cleanup, err := prepareExcelForDuckDB(path)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	book, err := excelize.OpenFile(converted)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Close() })
	rows, err := book.GetRows("销售")
	require.NoError(t, err)
	require.Equal(t, []string{"苹果", "3"}, rows[1])
}

func TestUniqueExcelSheetNameStripsReservedCharacters(t *testing.T) {
	used := map[string]struct{}{}
	first := uniqueExcelSheetName("a/b:c", 0, used)
	second := uniqueExcelSheetName("a/b:c", 1, used)
	require.Equal(t, "a_b_c", first)
	require.NotEqual(t, first, second)
	require.LessOrEqual(t, len([]rune(second)), 31)
}

func TestPrepareExcelForDuckDBLeavesXLSXAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.xlsx")
	writeWorkbook(t, path, map[string][][]any{
		"Sheet1": {{"a"}, {"b"}},
	}, []string{"Sheet1"})

	got, cleanup, err := prepareExcelForDuckDB(path)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, path, got)
}

func TestLoadFromExcelReadsLegacyXLS(t *testing.T) {
	db := newTestDuckDB(t)
	tool := &DataAnalysisTool{
		BaseTool:  dataAnalysisTool,
		db:        db,
		sessionID: "test-legacy-xls",
	}
	ctx := context.Background()
	schema, err := tool.LoadFromExcel(ctx, filepath.Join("testdata", "legacy.xls"), "t_legacy")
	require.NoError(t, err)
	t.Cleanup(func() { tool.Cleanup(ctx) })

	// Two data rows on 销售 and one on 库存.
	require.Equal(t, int64(3), schema.RowCount)
}

func TestPrepareExcelForDuckDBConvertsEdgeCases(t *testing.T) {
	converted, cleanup, err := prepareExcelForDuckDB(filepath.Join("testdata", "edge.xls"))
	require.NoError(t, err)
	t.Cleanup(cleanup)

	book, err := excelize.OpenFile(converted)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Close() })
	require.Equal(t, []string{"边界 Edge", "空表", "Sheet1"}, book.GetSheetList())

	value := func(cell string) string {
		v, err := book.GetCellValue("边界 Edge", cell)
		require.NoError(t, err)
		return v
	}
	require.Equal(t, "3", value("B1"))
	require.Equal(t, "-0.125", value("B3"), "negative RK values")
	require.Equal(t, "1234567890123", value("B4"))
	require.Equal(t, "0.0000001", value("B5"))
	require.Equal(t, "TRUE", value("B7"))
	require.Equal(t, "中文：苹果，梨🍐", value("B10"))
	require.Equal(t, strings.Repeat("x", 9000), value("B11"))
	require.Equal(t, "a\nb", value("B12"))
	require.Equal(t, "  pad  ", value("B13"), "surrounding whitespace survives")
	require.Equal(t, "45351", value("B21"), "dates stay serial numbers")
	require.Equal(t, "sparse far cell", value("F31"))
	require.Equal(t, "", value("B9"))

	empty, err := book.GetRows("空表")
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestLoadFromExcelReadsLegacyXLSEdgeCases(t *testing.T) {
	db := newTestDuckDB(t)
	tool := &DataAnalysisTool{
		BaseTool:  dataAnalysisTool,
		db:        db,
		sessionID: "test-legacy-xls-edge",
	}
	ctx := context.Background()
	_, err := tool.LoadFromExcel(ctx, filepath.Join("testdata", "edge.xls"), "t_edge")
	require.NoError(t, err)
	t.Cleanup(func() { tool.Cleanup(ctx) })

	// The header row is "int", "3"; DuckDB reads every cell as text.
	var neg, cjk string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT "3" FROM t_edge WHERE "int" = 'neg'`).Scan(&neg))
	require.Equal(t, "-0.125", neg)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT "3" FROM t_edge WHERE "int" = 'cjk'`).Scan(&cjk))
	require.Equal(t, "中文：苹果，梨🍐", cjk)
}

func TestPrepareExcelForDuckDBRejectsCorruptXLS(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "legacy.xls"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "corrupt.xls")
	require.NoError(t, os.WriteFile(path, data[:600], 0o644))

	_, cleanup, err := prepareExcelForDuckDB(path)
	cleanup()
	require.Error(t, err)
}

// BenchmarkConvertLegacyXLS measures the .xls -> .xlsx conversion. By default
// it converts testdata/edge.xls; set GENYEE_XLS_BENCH_DIR to a directory of
// larger workbooks (kept out of the repository) to benchmark those instead.
func BenchmarkConvertLegacyXLS(b *testing.B) {
	files := []string{filepath.Join("testdata", "edge.xls")}
	if dir := os.Getenv("GENYEE_XLS_BENCH_DIR"); dir != "" {
		found, err := filepath.Glob(filepath.Join(dir, "*.xls"))
		require.NoError(b, err)
		files = found
	}
	for _, file := range files {
		b.Run(filepath.Base(file), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out, err := convertLegacyXLSToXLSX(file)
				if err != nil {
					b.Fatal(err)
				}
				_ = os.Remove(out)
			}
		})
	}
}
