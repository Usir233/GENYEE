package legacyxls

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOracleCorpus compares every cell against reference dumps produced by
// the Python xlrd reader. It is opt-in because the corpus (including
// multi-megabyte workbooks) is kept outside the repository:
//
//	GENYEE_XLS_ORACLE_DIR=/path/to/corpus go test ./internal/infrastructure/legacyxls -run Oracle
//
// The directory holds <name>.xls files next to <name>.oracle.json files of the
// form {"order": [sheet...], "<sheet>": {"r,c": [kind, value]}} where kind is
// "s" (text), "n" (number or date serial), "b" (boolean) or "e" (error code).
func TestOracleCorpus(t *testing.T) {
	dir := os.Getenv("GENYEE_XLS_ORACLE_DIR")
	if dir == "" {
		t.Skip("set GENYEE_XLS_ORACLE_DIR to run the xlrd oracle comparison")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.xls"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(strings.TrimSuffix(file, ".xls") + ".oracle.json")
			if os.IsNotExist(err) {
				t.Skip("no oracle dump")
			}
			require.NoError(t, err)
			var oracle map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &oracle))
			var order []string
			require.NoError(t, json.Unmarshal(oracle["order"], &order))

			wb, err := Open(file)
			require.NoError(t, err)
			require.Equal(t, order, sheetNames(wb))

			for i, name := range order {
				var want map[string][2]any
				require.NoError(t, json.Unmarshal(oracle[name], &want))
				got := map[string]string{}
				for _, c := range wb.Sheets[i].Cells {
					got[fmt.Sprintf("%d,%d", c.Row, c.Col)] = c.Value
				}
				var mismatches []string
				for key, w := range want {
					g, ok := got[key]
					delete(got, key)
					if !ok {
						mismatches = append(mismatches, fmt.Sprintf("%s missing want=%v", key, w))
						continue
					}
					if !oracleMatches(w, g) {
						mismatches = append(mismatches, fmt.Sprintf("%s want=%v got=%q", key, w, g))
					}
				}
				for key, g := range got {
					mismatches = append(mismatches, fmt.Sprintf("%s extra got=%q", key, g))
				}
				sort.Strings(mismatches)
				if len(mismatches) > 20 {
					mismatches = mismatches[:20]
				}
				require.Empty(t, mismatches, "sheet %q", name)
			}
		})
	}
}

func oracleMatches(want [2]any, got string) bool {
	switch want[0] {
	case "s":
		return want[1] == got
	case "n":
		f, err := strconv.ParseFloat(got, 64)
		return err == nil && f == want[1].(float64)
	case "b":
		return boolErrText(byte(want[1].(float64)), false) == got
	case "e":
		return boolErrText(byte(want[1].(float64)), true) == got
	}
	return false
}
