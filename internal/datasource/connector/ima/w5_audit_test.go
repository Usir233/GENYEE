package ima

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/types"
)

func TestW5IMAAudit_NilIncrementalConfigAndFolderCycleProtection(t *testing.T) {
	conn := NewConnector()
	if _, _, err := conn.FetchIncremental(context.Background(), nil, nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig on nil FetchIncremental config, got %v", err)
	}

	var folderACalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := strings.TrimPrefix(r.URL.Path, apiBasePath+"/")
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch action {
		case "get_knowledge_list":
			folderID, _ := req["folder_id"].(string)
			var rawList []json.RawMessage
			add := func(id, title string, mt int32) {
				b, _ := json.Marshal(knowledgeInfo{MediaID: id, Title: title, MediaType: mt, ParentFolderID: folderID})
				rawList = append(rawList, b)
			}
			switch folderID {
			case "":
				add("folder_A", "FolderA", mediaTypeFolder)
			case "folder_A":
				if folderACalls.Add(1) > 4 {
					t.Errorf("infinite folder cycle in listAllKBFiles: folder_A listed %d times", folderACalls.Load())
					http.Error(w, "folder cycle", http.StatusInternalServerError)
					return
				}
				add("folder_B", "FolderB", mediaTypeFolder)
			case "folder_B":
				add("folder_A", "FolderA-Loop", mediaTypeFolder)
				add("m_note_1", "Note1", mediaTypeNote)
			}
			writeEnvelope(w, 0, "", getKnowledgeListResp{KnowledgeList: rawList, IsEnd: true})
		case "get_media_info":
			writeEnvelope(w, 0, "", getMediaInfoResp{
				MediaType:       mediaTypeNote,
				NotebookExtInfo: notebookExtInfo{NotebookID: "nb1"},
			})
		case "/openapi/note/v1/get_doc_content":
			writeEnvelope(w, 0, "", getDocContentResp{Content: "hello cycle"})
		default:
			if strings.HasSuffix(r.URL.Path, "/get_doc_content") {
				writeEnvelope(w, 0, "", getDocContentResp{Content: "hello cycle"})
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeIMA,
		Credentials: map[string]interface{}{
			"client_id": "cid_test",
			"api_key":   "key_test",
			"base_url":  srv.URL,
		},
		ResourceIDs: []string{"kb_cycle"},
	}

	items, err := conn.FetchAll(context.Background(), cfg, []string{"kb_cycle"})
	if err != nil {
		t.Fatalf("FetchAll failed on cyclic folder tree: %v", err)
	}
	if folderACalls.Load() != 1 {
		t.Fatalf("expected folder_A listed once, got %d", folderACalls.Load())
	}
	if len(items) != 1 || string(items[0].Content) != "hello cycle" {
		t.Fatalf("unexpected items from cyclic KB: %+v", items)
	}
}

func TestW5IMAAudit_BlocksUnsafeBareWebURLAndCredentialControlChars(t *testing.T) {
	secret := "ima-secret-api-key-w5"
	badCfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeIMA,
		Credentials: map[string]interface{}{
			"client_id": "cid_test",
			"api_key":   secret + "\r\nX-Injected: 1",
		},
	}
	if _, err := parseIMAConfig(badCfg); err == nil {
		t.Fatal("expected parseIMAConfig to reject control chars in api_key")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("parseIMAConfig leaked secret in error: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := strings.TrimPrefix(r.URL.Path, apiBasePath+"/")
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch action {
		case "get_knowledge_list":
			var rawList []json.RawMessage
			for _, f := range []knowledgeInfo{
				{MediaID: "m_ssrf", Title: "MetadataSSRF", MediaType: mediaTypeWeb},
				{MediaID: "m_file", Title: "LocalFile", MediaType: mediaTypeWeb},
				{MediaID: "m_user", Title: "UserInfo", MediaType: mediaTypeWeb},
			} {
				b, _ := json.Marshal(f)
				rawList = append(rawList, b)
			}
			writeEnvelope(w, 0, "", getKnowledgeListResp{KnowledgeList: rawList, IsEnd: true})
		case "get_media_info":
			mid, _ := req["media_id"].(string)
			u := ""
			switch mid {
			case "m_ssrf":
				u = "http://169.254.169.254/latest/meta-data/"
			case "m_file":
				u = "file:///etc/passwd"
			case "m_user":
				u = "http://user:secret@127.0.0.1/page"
			}
			writeEnvelope(w, 0, "", getMediaInfoResp{
				MediaType: mediaTypeWeb,
				URLInfo:   urlInfo{URL: u},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeIMA,
		Credentials: map[string]interface{}{
			"client_id": "cid_test",
			"api_key":   "key_test",
			"base_url":  srv.URL,
		},
		ResourceIDs: []string{"kb_ssrf"},
	}

	items, err := NewConnector().FetchAll(context.Background(), cfg, []string{"kb_ssrf"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected unsafe bare web URLs to be skipped, got %d items: %+v", len(items), items)
	}
}
