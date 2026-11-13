package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdateChunkPreservesOmittedFieldsAndRevision(t *testing.T) {
	for _, body := range []string{`{"content":"edited","expected_revision":3}`, `{"is_enabled":false,"expected_revision":3}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got, want map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(body), &want); err != nil {
					t.Fatal(err)
				}
				if got["expected_revision"] != want["expected_revision"] {
					t.Errorf("optimistic revision lost: %+v", got)
				}
				for _, field := range []string{"content", "is_enabled"} {
					_, supplied := want[field]
					_, sent := got[field]
					if supplied != sent {
						t.Errorf("unexpected update to omitted %s: %+v", field, got)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"success":true,"data":{"id":"chunk","content":"edited","is_enabled":true,"content_revision":4}}`)
			}))
			defer s.Close()
			var req UpdateChunkRequest
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatal(err)
			}
			got, err := NewClient(s.URL).UpdateChunk(context.Background(), "knowledge", "chunk", &req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			if fields["content_revision"] != float64(4) {
				t.Errorf("updated revision lost: %s", raw)
			}
		})
	}
}

func TestDeleteGeneratedQuestionUsesRegisteredRoute(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/chunks/by-id/chunk-1/questions" {
			http.NotFound(w, r)
			return
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["question_id"] != "question-1" {
			t.Errorf("wrong question: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer s.Close()
	if err := NewClient(s.URL).DeleteGeneratedQuestion(context.Background(), "chunk-1", "question-1"); err != nil {
		t.Fatal(err)
	}
}
