package legacyxls

import (
	"encoding/binary"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCellsAreSortedAndLastWriteWins(t *testing.T) {
	data := buildWorkbookFile(workbookSpec{
		sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
			b.rec(recNumber, cell(5, 2), f64(1))
			b.rec(recNumber, cell(0, 3), f64(2))
			b.rec(recNumber, cell(0, 1), f64(3))
			b.rec(recNumber, cell(5, 2), f64(4)) // overwrites the first record
			b.rec(recNumber, cell(2, 255), f64(5))
			b.rec(recNumber, cell(2, 256), f64(6)) // beyond the BIFF grid
		}}},
	})
	wb, err := readBytes(t, data)
	require.NoError(t, err)
	require.Equal(t, []Cell{
		{Row: 0, Col: 1, Value: "3"},
		{Row: 0, Col: 3, Value: "2"},
		{Row: 2, Col: 255, Value: "5"},
		{Row: 5, Col: 2, Value: "4"},
	}, wb.Sheets[0].Cells)
}

func TestOverlappingSheetOffsetsAreReadOnce(t *testing.T) {
	// Point many BoundSheet records at the same substream. Each sheet keeps
	// its name, but the shared cells are only attributed to the first.
	stream := buildStream(workbookSpec{
		sheets: []testSheet{
			{name: "real", records: func(b *biffBuilder) {
				for r := uint16(0); r < 100; r++ {
					b.rec(recNumber, cell(r, 0), f64(float64(r)))
				}
			}},
			{name: "alias1"},
			{name: "alias2"},
		},
	})
	// Rewrite the alias offsets to point at the first sheet's substream.
	pos := 0
	var offsets []int
	for {
		rec, next, ok := nextRecord(stream, pos)
		if !ok || rec.id == recEOF {
			break
		}
		if rec.id == recBoundSheet {
			offsets = append(offsets, pos+4)
		}
		pos = next
	}
	require.Len(t, offsets, 3)
	first := binary.LittleEndian.Uint32(stream[offsets[0]:])
	binary.LittleEndian.PutUint32(stream[offsets[1]:], first)
	binary.LittleEndian.PutUint32(stream[offsets[2]:], first+20)

	wb, err := parseStream(stream)
	require.NoError(t, err)
	require.Equal(t, []string{"real", "alias1", "alias2"}, sheetNames(wb))
	require.Len(t, wb.Sheets[0].Cells, 100)
	require.Empty(t, wb.Sheets[1].Cells)
	require.Empty(t, wb.Sheets[2].Cells)
}

func TestForgedCompoundHeaderCountsAreRejected(t *testing.T) {
	good := buildWorkbookFile(workbookSpec{sheets: []testSheet{{name: "S"}}})
	for _, off := range []int{40, 44, 64, 72} {
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint32(bad[off:], 0xFFFFFFF0)
		_, err := readBytes(t, bad)
		require.ErrorIs(t, err, ErrUnsupported, "header offset %d", off)
	}
	bad := append([]byte(nil), good...)
	binary.LittleEndian.PutUint16(bad[30:], 7) // illegal sector shift
	_, err := readBytes(t, bad)
	require.ErrorIs(t, err, ErrUnsupported)

	_, err = readBytes(t, good[:100])
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestForgedStreamSizeDoesNotOverAllocate(t *testing.T) {
	good := buildWorkbookFile(workbookSpec{sheets: []testSheet{{name: "S", records: func(b *biffBuilder) {
		b.rec(recNumber, cell(0, 0), f64(1))
	}}}})
	// The directory sector follows the data sectors; entry 1 is the stream.
	dataSectors := (len(good) - 512 - 512 - 512) / 512
	sizeOff := 512 + dataSectors*512 + 128 + 120
	bad := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(bad[sizeOff:], 0xFFFFFF00)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var wb *Workbook
	var err error
	require.NotPanics(t, func() { wb, err = readBytes(t, bad) })
	runtime.ReadMemStats(&after)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(64<<20),
		"a forged 4 GiB stream size must not drive the allocation")
	if err == nil {
		require.Equal(t, "1", cells(wb.Sheets[0])["A1"])
	}
}
