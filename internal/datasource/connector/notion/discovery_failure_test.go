package notion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/datasource"
)

func TestFetchIncrementalSearchFailure(t *testing.T) {
	for _, failPage := range []int{1, 2} {
		t.Run("page_"+strconv.Itoa(failPage), func(t *testing.T) {
			allowNotionTestServer(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/search" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if requests == failPage {
					http.Error(w, "access revoked", http.StatusUnauthorized)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"results": []interface{}{}, "has_more": true, "next_cursor": "second-page",
				})
			}))
			defer server.Close()
			config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page-1"})
			cursor := buildCursor(map[string]time.Time{"page-1": time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)})
			items, next, err := NewConnector().FetchIncremental(context.Background(), config, cursor)
			if !errors.Is(err, datasource.ErrInvalidCredentials) {
				t.Errorf("expected credential error, got %v", err)
			}
			if len(items) != 0 || next != nil {
				t.Errorf("failed discovery must return no items or cursor, got items=%+v cursor=%+v", items, next)
			}
			if requests != failPage {
				t.Errorf("expected %d requests, got %d", failPage, requests)
			}
		})
	}
}

func TestFetchIncrementalSearchMissingCursor(t *testing.T) {
	allowNotionTestServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"has_more":true,"next_cursor":null}`))
	}))
	defer server.Close()
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page-1"})
	cursor := buildCursor(map[string]time.Time{"page-1": time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)})

	items, next, err := NewConnector().FetchIncremental(context.Background(), config, cursor)
	if err == nil {
		t.Errorf("expected an error for incomplete search pagination")
	}
	if len(items) != 0 || next != nil {
		t.Errorf("incomplete discovery must return no items or cursor, got items=%+v cursor=%+v", items, next)
	}
}

func TestFetchIncrementalSuccessfulEmptySearch(t *testing.T) {
	allowNotionTestServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{}, "has_more": false,
		})
	}))
	defer server.Close()
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page-1"})
	cursor := buildCursor(map[string]time.Time{"page-1": time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)})
	items, next, err := NewConnector().FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatalf("expected successful discovery, got %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "page-1" || !items[0].IsDeleted {
		t.Fatalf("expected deletion of absent page-1, got %+v", items)
	}
	if next == nil {
		t.Fatal("expected updated cursor after successful discovery")
	}
}

func TestNotionDatabaseQueryFailurePreservesCursorAndSkipsDeletions(t *testing.T) {
	t.Run("transient_query_error_incremental", func(t *testing.T) {
		allowNotionTestServer(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/search":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"results":  []interface{}{truncationDataSourceRow()},
					"has_more": false,
				})
			case "/v1/data_sources/ds-1":
				_ = json.NewEncoder(w).Encode(windowDataSourceInfo())
			case "/v1/data_sources/ds-1/query":
				w.Header().Set("Retry-After", "0.01")
				http.Error(w, "rate limited", http.StatusTooManyRequests)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})
		items, next, err := NewConnector().FetchIncremental(context.Background(), config, truncationPrevCursor())
		if err == nil {
			t.Fatalf("expected error on database query failure, got items=%+v cursor=%+v", items, next)
		}
		if len(items) != 0 || next != nil {
			t.Fatalf(
				"failed database query must not emit tombstones or advance cursor: items=%+v cursor=%+v",
				items, next,
			)
		}
	})

	t.Run("missing_next_cursor_in_datasource_query", func(t *testing.T) {
		allowNotionTestServer(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/search":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"results":  []interface{}{truncationDataSourceRow()},
					"has_more": false,
				})
			case "/v1/data_sources/ds-1":
				_ = json.NewEncoder(w).Encode(windowDataSourceInfo())
			case "/v1/data_sources/ds-1/query":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"object":      "list",
					"results":     []interface{}{truncationRecord("r1")},
					"has_more":    true,
					"next_cursor": nil,
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})
		items, next, err := NewConnector().FetchIncremental(context.Background(), config, truncationPrevCursor())
		if err == nil {
			t.Fatalf(
				"expected error when data_source query sets has_more=true without next_cursor, got items=%+v",
				items,
			)
		}
		if len(items) != 0 || next != nil {
			t.Fatalf(
				"incomplete data_source query must not delete records or advance cursor: items=%+v cursor=%+v",
				items, next,
			)
		}
	})

	t.Run("transient_error_full_sync", func(t *testing.T) {
		allowNotionTestServer(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer server.Close()

		config := makeNotionConfig(&Config{APIKey: "bad-token"}, server.URL, []string{"page-1"})
		items, err := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
		if !errors.Is(err, datasource.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials from FetchAll, got items=%+v err=%v", items, err)
		}
	})
}
