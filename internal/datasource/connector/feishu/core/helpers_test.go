package core

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	secutils "github.com/Usir233/GENYEE/internal/utils"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost,open.feishu.cn,open.larksuite.com")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func cellBlk(id string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeTableCell, Children: []string{id + "_txt"}}
}

func cellTextBlk(id, text string) DocxBlock {
	return DocxBlock{BlockID: id + "_txt", BlockType: BlockTypeText, Text: txt(text)}
}

func tableBlk(id string, cols int, cellIDs ...string) DocxBlock {
	b := DocxBlock{BlockID: id, BlockType: BlockTypeTable}
	b.Table = &BlockTable{Cells: cellIDs}
	b.Table.Property = &BlockTableProperty{ColumnSize: cols}
	return b
}
