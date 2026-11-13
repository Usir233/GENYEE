package docparser

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func paddleCloudFixture(t *testing.T, imageHandler http.HandlerFunc, resultHandler http.HandlerFunc) *PaddleOCRVLCloudReader {
	t.Helper()
	var baseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, `{"data":{"jobId":"fixture"}}`)
		case "/jobs/fixture":
			_, _ = fmt.Fprintf(w, `{"data":{"state":"done","resultUrl":{"jsonUrl":%q}}}`, baseURL+"/result")
		case "/result":
			if resultHandler != nil {
				resultHandler(w, r)
			}
			_, _ = fmt.Fprintf(w, `{"result":{"layoutParsingResults":[{"markdown":{"text":"before ![page](images/page.png) after","images":{"images/page.png":%q}}}]}}`, baseURL+"/image")
		case "/image":
			if imageHandler != nil {
				imageHandler(w, r)
			} else {
				_, _ = io.WriteString(w, "image fixture")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	baseURL = server.URL
	t.Cleanup(server.Close)
	return NewPaddleOCRVLCloudReader(map[string]string{
		"paddleocr_vl_cloud_token": "isolated-test-token", "paddleocr_vl_cloud_base_url": baseURL + "/jobs",
	})
}

func TestPaddleCloudDownloadsHonorCancellation(t *testing.T) {
	allowLoopbackSSRF(t)
	for _, stage := range []string{"result", "image"} {
		for _, bodyStarted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/body_started=%t", stage, bodyStarted), func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				block := func(w http.ResponseWriter, r *http.Request) {
					if bodyStarted {
						_, _ = io.WriteString(w, " ")
						w.(http.Flusher).Flush()
					}
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
				var imageHandler, resultHandler http.HandlerFunc
				if stage == "result" {
					resultHandler = block
				} else {
					imageHandler = block
				}
				reader := paddleCloudFixture(t, imageHandler, resultHandler)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				type outcome struct {
					result *types.ReadResult
					err    error
				}
				done, finished := make(chan outcome, 1), make(chan struct{})
				go func() {
					defer close(finished)
					result, err := reader.Read(ctx, &types.ReadRequest{FileName: "fixture.pdf", FileType: "pdf", FileContent: []byte("fixture")})
					done <- outcome{result, err}
				}()
				defer func() {
					close(release)
					select {
					case <-finished:
					case <-time.After(3 * time.Second):
						t.Error("reader did not stop after fixture release")
					}
				}()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("download never started")
				}
				cancel()
				select {
				case got := <-done:
					require.ErrorIs(t, got.err, context.Canceled)
					require.Nil(t, got.result, "cancellation must not publish partial text/images as success")
				case <-time.After(time.Second):
					t.Fatal("canceled parser remains blocked in " + stage + " download")
				}
			})
		}
	}
}

func TestPaddleCloudDownloadsKeepSuccessAndOptionalImageFailure(t *testing.T) {
	allowLoopbackSSRF(t)
	for _, failImage := range []bool{false, true} {
		t.Run(fmt.Sprintf("image_failure=%t", failImage), func(t *testing.T) {
			var handler http.HandlerFunc
			if failImage {
				handler = func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
			}
			reader := paddleCloudFixture(t, handler, nil)
			result, err := reader.Read(context.Background(), &types.ReadRequest{FileName: "fixture.pdf", FileType: "pdf", FileContent: []byte("fixture")})
			require.NoError(t, err)
			require.Contains(t, result.MarkdownContent, "before ![page](images/page.png) after")
			if failImage {
				require.Empty(t, result.ImageRefs)
			} else {
				require.Len(t, result.ImageRefs, 1)
				require.Equal(t, []byte("image fixture"), result.ImageRefs[0].ImageData)
			}
		})
	}
}
