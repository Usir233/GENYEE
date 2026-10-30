// Package legacyxls reads cell values from legacy binary Excel workbooks:
// BIFF8 (Excel 97-2003) and BIFF5/BIFF7 (Excel 5.0/95), stored in an OLE2
// compound file.
//
// It is a small clean-room implementation written against Microsoft's public
// [MS-XLS] specification and the OpenOffice.org "Excel File Format"
// documentation. It only extracts what a text-oriented consumer needs: sheet
// names and the displayed value of every non-empty cell, as text.
//
// Value rendering:
//   - numbers use the shortest decimal form that round-trips, without an
//     exponent ("3", "12.5", "-0.125", "0.0000001");
//   - number formats are not applied, so dates stay as Excel serial numbers
//     and percentages as fractions;
//   - booleans become "TRUE"/"FALSE" and error values their Excel literal
//     ("#DIV/0!", "#N/A", ...);
//   - formula cells yield the result cached in the file;
//   - merged ranges keep their value in the top-left cell only.
//
// Malformed input never panics: every read is bounds-checked and a recovered
// panic is reported as an error.
package legacyxls

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
)

// Cell is one non-empty cell at a zero-based row and column.
type Cell struct {
	Row, Col int
	Value    string
}

// Sheet is one worksheet. Cells holds its non-empty cells sorted by row, then
// column, each position at most once. Storage is sparse, so memory follows
// the number of cells rather than the sheet's extent.
type Sheet struct {
	Name  string
	Cells []Cell
}

// Workbook holds the worksheets of a workbook in their tab order.
type Workbook struct {
	Sheets []Sheet
}

var (
	// ErrEncrypted is returned for password-protected (encrypted) workbooks.
	ErrEncrypted = errors.New("legacyxls: workbook is password protected")
	// ErrUnsupported is returned when the file holds no BIFF5/BIFF8 workbook stream.
	ErrUnsupported = errors.New("legacyxls: not a BIFF5 or BIFF8 workbook")
)

// Grid limits of BIFF5 and BIFF8 worksheets. Cells outside them cannot be
// written by Excel and are ignored.
const (
	maxRows = 1 << 16
	maxCols = 1 << 8
)

// Record identifiers ([MS-XLS] 2.3).
const (
	recFormula    = 0x0006
	recEOF        = 0x000A
	recFilePass   = 0x002F
	recContinue   = 0x003C
	recCodePage   = 0x0042
	recBoundSheet = 0x0085
	recMulRK      = 0x00BD
	recRString    = 0x00D6
	recSST        = 0x00FC
	recLabelSST   = 0x00FD
	recNumber     = 0x0203
	recLabel      = 0x0204
	recBoolErr    = 0x0205
	recString     = 0x0207
	recRK         = 0x027E
	recBOF        = 0x0809
)

const (
	biff5 = 0x0500
	biff8 = 0x0600
)

// Open reads the workbook stored at path.
func Open(path string) (*Workbook, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return Read(f, info.Size())
}

// Read reads a workbook from an OLE2 compound file of the given size.
func Read(ra io.ReaderAt, size int64) (wb *Workbook, err error) {
	defer func() {
		if r := recover(); r != nil {
			wb, err = nil, fmt.Errorf("legacyxls: malformed workbook: %v", r)
		}
	}()
	if err := checkCompoundHeader(ra, size); err != nil {
		return nil, err
	}
	stream, err := workbookStream(ra, size)
	if err != nil {
		return nil, err
	}
	return parseStream(stream)
}

// checkCompoundHeader rejects compound files whose header declares more
// sectors than the file can hold, before the container parser sizes any
// allocation from those untrusted counts.
func checkCompoundHeader(ra io.ReaderAt, size int64) error {
	const headerSize = 512
	if size < headerSize {
		return fmt.Errorf("%w: file too small for a compound document", ErrUnsupported)
	}
	var h [headerSize]byte
	if _, err := ra.ReadAt(h[:], 0); err != nil {
		return fmt.Errorf("legacyxls: read compound file header: %w", err)
	}
	sectorShift := binary.LittleEndian.Uint16(h[30:])
	if sectorShift != 9 && sectorShift != 12 {
		return fmt.Errorf("%w: invalid sector size", ErrUnsupported)
	}
	sectors := uint64(size) >> sectorShift
	for _, off := range []int{40, 44, 64, 72} { // directory, FAT, mini FAT, DIFAT sector counts
		if uint64(binary.LittleEndian.Uint32(h[off:])) > sectors {
			return fmt.Errorf("%w: corrupt compound file header", ErrUnsupported)
		}
	}
	return nil
}

// workbookStream returns the contents of the "Workbook" (BIFF8) or "Book"
// (BIFF5) stream. When both exist the BIFF8 one wins, as in Excel.
func workbookStream(ra io.ReaderAt, size int64) ([]byte, error) {
	doc, err := mscfb.New(ra)
	if err != nil {
		return nil, fmt.Errorf("legacyxls: read compound file: %w", err)
	}
	var book *mscfb.File
	for entry, err := doc.Next(); err == nil; entry, err = doc.Next() {
		if len(entry.Path) != 0 {
			continue // only streams directly under the root storage
		}
		switch {
		case strings.EqualFold(entry.Name, "Workbook"):
			return readStream(entry, size)
		case strings.EqualFold(entry.Name, "Book") && book == nil:
			book = entry
		}
	}
	if book == nil {
		return nil, ErrUnsupported
	}
	return readStream(book, size)
}

// readStream reads a stream. Its declared size is untrusted, so neither the
// preallocation nor the read may exceed the size of the file itself.
func readStream(f *mscfb.File, fileSize int64) ([]byte, error) {
	if f.Size <= 0 {
		return nil, ErrUnsupported
	}
	limit := min(f.Size, fileSize)
	buf := bytes.NewBuffer(make([]byte, 0, int(limit)))
	if _, err := io.Copy(buf, io.LimitReader(f, limit)); err != nil {
		return nil, fmt.Errorf("legacyxls: read workbook stream: %w", err)
	}
	return buf.Bytes(), nil
}

// record is one BIFF record; data aliases the stream.
type record struct {
	id   uint16
	data []byte
}

// nextRecord decodes the record at offset pos and returns the offset of the
// following one. ok is false at the end of the stream or on a truncated header.
func nextRecord(stream []byte, pos int) (rec record, next int, ok bool) {
	if pos < 0 || pos+4 > len(stream) {
		return record{}, pos, false
	}
	id := binary.LittleEndian.Uint16(stream[pos:])
	size := int(binary.LittleEndian.Uint16(stream[pos+2:]))
	start := pos + 4
	end := start + size
	if end > len(stream) {
		end = len(stream) // tolerate a truncated final record
	}
	return record{id: id, data: stream[start:end]}, end, true
}

type boundSheet struct {
	name   string
	offset int
}

type parser struct {
	stream  []byte
	version uint16
	decoder *textDecoder
	sst     []string
}

func parseStream(stream []byte) (*Workbook, error) {
	first, _, ok := nextRecord(stream, 0)
	if !ok || first.id != recBOF || len(first.data) < 4 {
		return nil, ErrUnsupported
	}
	p := &parser{stream: stream, decoder: newTextDecoder(1252)}
	switch v := binary.LittleEndian.Uint16(first.data); v {
	case biff8:
		p.version = biff8
	case biff5:
		p.version = biff5
	default:
		return nil, fmt.Errorf("%w (BOF version 0x%04X)", ErrUnsupported, v)
	}
	sheets, err := p.parseGlobals()
	if err != nil {
		return nil, err
	}
	wb := &Workbook{Sheets: make([]Sheet, len(sheets))}
	// Parse substreams in stream order and never re-read bytes that already
	// belong to another sheet: forged BoundSheet offsets pointing into each
	// other would otherwise make parsing quadratic.
	order := make([]int, len(sheets))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(sheets[a].offset, sheets[b].offset)
	})
	parsedUpTo := 0
	for _, i := range order {
		wb.Sheets[i].Name = sheets[i].name
		if sheets[i].offset < parsedUpTo {
			continue // overlaps a sheet already read: leave it empty
		}
		cells, end := p.parseSheet(sheets[i].offset)
		wb.Sheets[i].Cells = cells
		parsedUpTo = max(parsedUpTo, end)
	}
	return wb, nil
}

// parseGlobals walks the workbook globals substream up to its EOF record.
func (p *parser) parseGlobals() ([]boundSheet, error) {
	var sheets []boundSheet
	pos := 0
	for {
		rec, next, ok := nextRecord(p.stream, pos)
		if !ok {
			return sheets, nil
		}
		pos = next
		switch rec.id {
		case recEOF:
			return sheets, nil
		case recFilePass:
			return nil, ErrEncrypted
		case recCodePage:
			if len(rec.data) >= 2 {
				p.decoder = newTextDecoder(binary.LittleEndian.Uint16(rec.data))
			}
		case recBoundSheet:
			if bs, ok := p.parseBoundSheet(rec.data); ok {
				sheets = append(sheets, bs)
			}
		case recSST:
			segments := [][]byte{rec.data}
			for {
				cont, after, ok := nextRecord(p.stream, pos)
				if !ok || cont.id != recContinue {
					break
				}
				segments = append(segments, cont.data)
				pos = after
			}
			p.sst = parseSST(segments)
		}
	}
}

// parseBoundSheet decodes a BoundSheet8 record ([MS-XLS] 2.4.28). Only
// worksheets are returned; chart sheets, macro sheets and VBA modules carry
// no cell table.
func (p *parser) parseBoundSheet(data []byte) (boundSheet, bool) {
	if len(data) < 7 {
		return boundSheet{}, false
	}
	offset := int(binary.LittleEndian.Uint32(data))
	if sheetType := data[5]; sheetType != 0 {
		return boundSheet{}, false
	}
	cch := int(data[6])
	var name string
	if p.version == biff8 {
		if len(data) < 8 {
			return boundSheet{}, false
		}
		name, _ = decodeChars(data[8:], cch, data[7]&0x01 != 0)
	} else {
		name = p.decoder.decode(clip(data[7:], cch))
	}
	return boundSheet{name: name, offset: offset}, true
}

// sheetBuilder accumulates the cells of one worksheet.
type sheetBuilder struct {
	cells  []Cell
	sorted bool // cells are in strictly increasing (row, col) order so far
}

func (s *sheetBuilder) set(row, col int, value string) {
	if value == "" || row < 0 || row >= maxRows || col < 0 || col >= maxCols {
		return
	}
	if n := len(s.cells); n > 0 {
		last := s.cells[n-1]
		if row < last.Row || row == last.Row && col <= last.Col {
			s.sorted = false
		}
	}
	s.cells = append(s.cells, Cell{Row: row, Col: col, Value: value})
}

// result returns the cells sorted by position. When a position was written
// more than once the last record wins, as when Excel loads the file.
func (s *sheetBuilder) result() []Cell {
	if s.sorted || len(s.cells) < 2 {
		return s.cells
	}
	slices.SortStableFunc(s.cells, func(a, b Cell) int {
		if a.Row != b.Row {
			return cmp.Compare(a.Row, b.Row)
		}
		return cmp.Compare(a.Col, b.Col)
	})
	out := s.cells[:0]
	for i, c := range s.cells {
		if i+1 < len(s.cells) && s.cells[i+1].Row == c.Row && s.cells[i+1].Col == c.Col {
			continue
		}
		out = append(out, c)
	}
	return out
}

// parseSheet reads the cell records of the worksheet substream at offset and
// returns them with the stream offset just past the substream. Substreams
// embedded in the sheet (charts) are skipped.
func (p *parser) parseSheet(offset int) ([]Cell, int) {
	rec, pos, ok := nextRecord(p.stream, offset)
	if !ok || rec.id != recBOF {
		return nil, offset
	}
	sheet := sheetBuilder{sorted: true}
	depth := 0
	// pendingString is the cell of a string FORMULA whose value follows in
	// the next STRING record.
	pendingString := false
	var pendingRow, pendingCol int
	for {
		rec, next, ok := nextRecord(p.stream, pos)
		if !ok {
			break
		}
		pos = next
		switch rec.id {
		case recBOF:
			depth++
			continue
		case recEOF:
			if depth == 0 {
				return sheet.result(), pos
			}
			depth--
			continue
		}
		if depth > 0 {
			continue
		}
		d := rec.data
		switch rec.id {
		case recLabelSST:
			if len(d) >= 10 {
				if i := binary.LittleEndian.Uint32(d[6:]); int64(i) < int64(len(p.sst)) {
					sheet.set(cellRef(d), cellCol(d), p.sst[i])
				}
			}
		case recLabel, recRString:
			if len(d) >= 8 {
				sheet.set(cellRef(d), cellCol(d), p.readString(d[6:]))
			}
		case recNumber:
			if len(d) >= 14 {
				v := math.Float64frombits(binary.LittleEndian.Uint64(d[6:]))
				sheet.set(cellRef(d), cellCol(d), formatNumber(v))
			}
		case recRK:
			if len(d) >= 10 {
				sheet.set(cellRef(d), cellCol(d), formatNumber(decodeRK(binary.LittleEndian.Uint32(d[6:]))))
			}
		case recMulRK:
			if len(d) >= 6 {
				row, col := cellRef(d), cellCol(d)
				for i := 4; i+6 <= len(d)-2; i += 6 {
					sheet.set(row, col, formatNumber(decodeRK(binary.LittleEndian.Uint32(d[i+2:]))))
					col++
				}
			}
		case recBoolErr:
			if len(d) >= 8 {
				sheet.set(cellRef(d), cellCol(d), boolErrText(d[6], d[7] != 0))
			}
		case recFormula:
			pendingString = false
			if len(d) < 14 {
				break
			}
			row, col := cellRef(d), cellCol(d)
			result := d[6:14]
			if result[6] != 0xFF || result[7] != 0xFF {
				sheet.set(row, col, formatNumber(math.Float64frombits(binary.LittleEndian.Uint64(result))))
				break
			}
			switch result[0] {
			case 0: // string: the value is in the STRING record that follows
				pendingString, pendingRow, pendingCol = true, row, col
			case 1:
				sheet.set(row, col, boolErrText(result[2], false))
			case 2:
				sheet.set(row, col, boolErrText(result[2], true))
			}
		case recString:
			if !pendingString {
				break
			}
			pendingString = false
			segments := [][]byte{d}
			for {
				cont, after, ok := nextRecord(p.stream, pos)
				if !ok || cont.id != recContinue {
					break
				}
				segments = append(segments, cont.data)
				pos = after
			}
			sheet.set(pendingRow, pendingCol, p.readContinuedString(segments))
		}
	}
	return sheet.result(), pos
}

func cellRef(d []byte) int { return int(binary.LittleEndian.Uint16(d)) }
func cellCol(d []byte) int { return int(binary.LittleEndian.Uint16(d[2:])) }

// readString decodes the string of a LABEL or RSTRING record: an
// XLUnicodeString in BIFF8, a 16-bit length plus code-page bytes in BIFF5.
func (p *parser) readString(d []byte) string {
	if len(d) < 2 {
		return ""
	}
	cch := int(binary.LittleEndian.Uint16(d))
	if p.version != biff8 {
		return p.decoder.decode(clip(d[2:], cch))
	}
	if len(d) < 3 {
		return ""
	}
	s, _ := decodeChars(d[3:], cch, d[2]&0x01 != 0)
	return s
}

// readContinuedString decodes a STRING record that may span CONTINUE records.
func (p *parser) readContinuedString(segments [][]byte) string {
	if p.version != biff8 {
		var all []byte
		for _, s := range segments {
			all = append(all, s...)
		}
		return p.readString(all)
	}
	r := &segmentReader{segments: segments}
	cch, ok := r.u16()
	if !ok {
		return ""
	}
	flags, ok := r.u8()
	if !ok {
		return ""
	}
	return r.chars(int(cch), flags&0x01 != 0)
}

// parseSST decodes the shared string table ([MS-XLS] 2.4.265) from the SST
// record and its CONTINUE records.
func parseSST(segments [][]byte) []string {
	r := &segmentReader{segments: segments}
	if _, ok := r.u32(); !ok { // cstTotal
		return nil
	}
	unique, ok := r.u32()
	if !ok {
		return nil
	}
	// Every string takes at least 3 bytes, which bounds a forged count.
	limit := r.remaining() / 3
	n := int(min(int64(unique), int64(limit)))
	sst := make([]string, 0, n)
	for i := 0; i < int(unique); i++ {
		s, ok := r.richExtendedString()
		if !ok {
			break
		}
		sst = append(sst, s)
	}
	return sst
}

// segmentReader reads a logical record split across CONTINUE records.
type segmentReader struct {
	segments [][]byte
	seg      int
	pos      int
}

func (r *segmentReader) remaining() int {
	n := 0
	for i := r.seg; i < len(r.segments); i++ {
		n += len(r.segments[i])
	}
	return n - r.pos
}

// advance moves to the next segment once the current one is exhausted.
func (r *segmentReader) advance() bool {
	for r.seg < len(r.segments) && r.pos >= len(r.segments[r.seg]) {
		r.seg++
		r.pos = 0
	}
	return r.seg < len(r.segments)
}

func (r *segmentReader) u8() (byte, bool) {
	if !r.advance() {
		return 0, false
	}
	b := r.segments[r.seg][r.pos]
	r.pos++
	return b, true
}

func (r *segmentReader) u16() (uint16, bool) {
	lo, ok1 := r.u8()
	hi, ok2 := r.u8()
	return uint16(lo) | uint16(hi)<<8, ok1 && ok2
}

func (r *segmentReader) u32() (uint32, bool) {
	lo, ok1 := r.u16()
	hi, ok2 := r.u16()
	return uint32(lo) | uint32(hi)<<16, ok1 && ok2
}

func (r *segmentReader) skip(n int) bool {
	for n > 0 {
		if !r.advance() {
			return false
		}
		step := min(n, len(r.segments[r.seg])-r.pos)
		r.pos += step
		n -= step
	}
	return true
}

// chars reads cch characters. When the character array crosses into a
// CONTINUE record, that record starts with a fresh option byte whose low bit
// selects compressed (8-bit) or uncompressed (UTF-16) characters for the rest.
func (r *segmentReader) chars(cch int, high bool) string {
	var units []uint16
	var latin []byte
	wide := high
	for cch > 0 && r.seg < len(r.segments) {
		seg := r.segments[r.seg]
		if r.pos >= len(seg) {
			// The character array continues in the next CONTINUE record,
			// which starts with its own option byte.
			r.seg++
			r.pos = 0
			if r.seg >= len(r.segments) {
				break
			}
			if seg = r.segments[r.seg]; len(seg) == 0 {
				continue
			}
			wide = seg[0]&0x01 != 0
			r.pos = 1
			continue
		}
		avail := len(seg) - r.pos
		if wide {
			n := min(cch, avail/2)
			if n == 0 {
				r.pos = len(seg) // a dangling odd byte cannot form a character
				continue
			}
			if latin != nil {
				for _, b := range latin {
					units = append(units, uint16(b))
				}
				latin = nil
			}
			for i := 0; i < n; i++ {
				units = append(units, binary.LittleEndian.Uint16(seg[r.pos+2*i:]))
			}
			r.pos += 2 * n
			cch -= n
		} else {
			n := min(cch, avail)
			if units != nil {
				for _, b := range seg[r.pos : r.pos+n] {
					units = append(units, uint16(b))
				}
			} else {
				latin = append(latin, seg[r.pos:r.pos+n]...)
			}
			r.pos += n
			cch -= n
		}
	}
	if units == nil {
		return latin1(latin)
	}
	return string(utf16.Decode(units))
}

// richExtendedString reads one XLUnicodeRichExtendedString ([MS-XLS] 2.5.293).
func (r *segmentReader) richExtendedString() (string, bool) {
	cch, ok := r.u16()
	if !ok {
		return "", false
	}
	flags, ok := r.u8()
	if !ok {
		return "", false
	}
	var runs, ext int
	if flags&0x08 != 0 { // fRichSt
		n, ok := r.u16()
		if !ok {
			return "", false
		}
		runs = int(n)
	}
	if flags&0x04 != 0 { // fExtSt
		n, ok := r.u32()
		if !ok {
			return "", false
		}
		ext = int(int32(n))
		if ext < 0 {
			return "", false
		}
	}
	s := r.chars(int(cch), flags&0x01 != 0)
	if !r.skip(4*runs) || !r.skip(ext) {
		// The formatting tail is truncated; the text itself is complete.
		return s, true
	}
	return s, true
}

// decodeChars decodes cch characters from a single buffer, either compressed
// (one byte per character, the high byte being zero) or UTF-16LE.
func decodeChars(b []byte, cch int, high bool) (string, int) {
	if high {
		n := min(cch, len(b)/2)
		units := make([]uint16, n)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(b[2*i:])
		}
		return string(utf16.Decode(units)), 2 * n
	}
	n := min(cch, len(b))
	return latin1(b[:n]), n
}

// latin1 widens compressed BIFF8 characters: each byte is a code point in
// U+0000..U+00FF.
func latin1(b []byte) string {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

func clip(b []byte, n int) []byte {
	if n < 0 {
		return nil
	}
	if n > len(b) {
		n = len(b)
	}
	return b[:n]
}

// decodeRK decodes an RkNumber ([MS-XLS] 2.5.217): bit 0 means the value was
// multiplied by 100, bit 1 selects a signed 30-bit integer over the upper 30
// bits of an IEEE double.
func decodeRK(rk uint32) float64 {
	var v float64
	if rk&0x02 != 0 {
		v = float64(int32(rk) >> 2) // arithmetic shift keeps the sign
	} else {
		v = math.Float64frombits(uint64(rk&^0x03) << 32)
	}
	if rk&0x01 != 0 {
		v /= 100
	}
	return v
}

// formatNumber renders v as the shortest decimal that round-trips, without
// an exponent. Negative zero is rendered as "0".
func formatNumber(v float64) string {
	if v == 0 {
		return "0"
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "#NUM!"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// boolErrText renders a Boolean or error cell value ([MS-XLS] 2.5.10 Bes).
func boolErrText(v byte, isError bool) string {
	if !isError {
		if v != 0 {
			return "TRUE"
		}
		return "FALSE"
	}
	switch v {
	case 0x00:
		return "#NULL!"
	case 0x07:
		return "#DIV/0!"
	case 0x0F:
		return "#VALUE!"
	case 0x17:
		return "#REF!"
	case 0x1D:
		return "#NAME?"
	case 0x24:
		return "#NUM!"
	case 0x2A:
		return "#N/A"
	case 0x2B:
		return "#GETTING_DATA"
	default:
		return "#N/A"
	}
}
