package legacyxls

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenContinuedLabelAndRichString(t *testing.T) {
	for _, recordID := range []uint16{recLabel, recRString} {
		for _, tc := range []struct {
			name    string
			version uint16
			first   string
			rest    string
		}{
			{"biff8-compressed", biff8, strings.Repeat("a", 8215), strings.Repeat("b", 785)},
			{"biff8-wide", biff8, strings.Repeat("中", 4107), strings.Repeat("文", 893)},
			{"biff8-width-switch", biff8, strings.Repeat("a", 8215), "中文"},
			{"biff5", biff5, strings.Repeat("a", 2072), strings.Repeat("b", 928)},
		} {
			t.Run(fmt.Sprintf("%s/record-%04x", tc.name, recordID), func(t *testing.T) {
				want := tc.first + tc.rest
				stream := buildStream(workbookSpec{
					version: tc.version,
					sheets: []testSheet{{name: "LongText", records: func(b *biffBuilder) {
						var formatting []byte
						if recordID == recRString {
							formatting = u16(0)
							if tc.version == biff5 {
								formatting = u8(0)
							}
						}
						if tc.version == biff8 {
							flag, first, firstLen := chars8(tc.first)
							contFlag, rest, restLen := chars8(tc.rest)
							b.rec(recordID, cell(0, 0), u16(uint16(firstLen+restLen)), u8(flag), first)
							b.rec(recContinue, u8(contFlag), rest, formatting)
						} else {
							b.rec(recordID, cell(0, 0), u16(uint16(len(want))), []byte(tc.first))
							b.rec(recContinue, []byte(tc.rest), formatting)
						}
						// The following cell must remain readable after consuming all
						// continuation records belonging to the text cell.
						b.rec(recNumber, cell(1, 0), f64(42))
					}}},
				})
				file := filepath.Join(t.TempDir(), "continued.xls")
				require.NoError(t, os.WriteFile(file, buildCompoundFile("Workbook", stream), 0o600))
				wb, err := Open(file)
				require.NoError(t, err)
				require.Len(t, wb.Sheets, 1)
				require.Equal(t, []Cell{{Row: 0, Col: 0, Value: want}, {Row: 1, Col: 0, Value: "42"}}, wb.Sheets[0].Cells)
			})
		}
	}
}
