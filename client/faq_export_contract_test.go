package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFAQExportPreservesHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":1002,"message":"export refused"}`)
			}))
			defer s.Close()
			data, err := NewClient(s.URL).ExportFAQEntries(context.Background(), "kb")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code != 1002 || data != nil {
				t.Fatalf("data=%q err=%v; want typed HTTP %d error", data, err, status)
			}
		})
	}
}

func TestFAQExportPreservesCSVBytes(t *testing.T) {
	const body = "\xef\xbb\xbfQuestion,Answer\nquestion,answer\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	defer s.Close()
	data, err := NewClient(s.URL).ExportFAQEntries(context.Background(), "kb")
	if err != nil || string(data) != body {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
