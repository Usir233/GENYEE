package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/models/utils/ollama"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	ollamaapi "github.com/ollama/ollama/api"
	"github.com/stretchr/testify/require"
)

func ollamaImageTestClient(t *testing.T, chatHandler http.HandlerFunc) *OllamaChat {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/api/tags":
			_, _ = fmt.Fprint(w, `{"models":[{"name":"m:latest"}]}`)
		case "/api/chat":
			chatHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("OLLAMA_OPTIONAL", "")
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	svc, err := ollama.GetOllamaService()
	require.NoError(t, err)
	return &OllamaChat{modelName: "m", ollamaService: svc}
}

// Drain a stream even on the original implementation, so a failing regression
// does not leave its error-send goroutine behind after the fixture is released.
func runOllamaImageChat(ctx context.Context, c *OllamaChat, messages []Message, stream bool) error {
	if !stream {
		_, err := c.Chat(ctx, messages, nil)
		return err
	}
	chunks, err := c.ChatStream(ctx, messages, nil)
	if err != nil {
		return err
	}
	for chunk := range chunks {
		if chunk.ResponseType == types.ResponseTypeError {
			err = errors.New(chunk.Content)
		}
	}
	return err
}

func TestOllamaImageRequestCancellation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, bodyStarted := range []bool{false, true} {
			for _, deadline := range []bool{false, true} {
				name := fmt.Sprintf("stream=%t/body_started=%t/deadline=%t", stream, bodyStarted, deadline)
				t.Run(name, func(t *testing.T) {
					var chatCalls, laterImages atomic.Int32
					c := ollamaImageTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
						chatCalls.Add(1)
						_, _ = fmt.Fprint(w, `{"message":{"content":"unexpected"},"done":true}`)
					})
					reached, release, requestCancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
					var once sync.Once
					unblock := func() { once.Do(func() { close(release) }) }
					images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/later" {
							laterImages.Add(1)
							return
						}
						if bodyStarted {
							w.WriteHeader(http.StatusOK)
							_, _ = fmt.Fprint(w, "partial image")
							w.(http.Flusher).Flush()
						}
						close(reached)
						select {
						case <-r.Context().Done():
							close(requestCancelled)
						case <-release:
						}
					}))
					defer images.Close()
					defer unblock()
					ctx, cancel := context.WithCancel(t.Context())
					wantErr := context.Canceled
					if deadline {
						cancel()
						ctx, cancel = context.WithTimeout(t.Context(), 500*time.Millisecond)
						wantErr = context.DeadlineExceeded
					}
					defer cancel()
					message := Message{Role: "user", Images: []string{images.URL, images.URL + "/later"}}
					if bodyStarted {
						message.Images = nil
						message.MultiContent = []api.MessageContentPart{
							{Type: "image_url", ImageURL: &api.ImageURL{URL: images.URL}},
							{Type: "image_url", ImageURL: &api.ImageURL{URL: images.URL + "/later"}},
						}
					}
					done := make(chan error, 1)
					go func() { done <- runOllamaImageChat(ctx, c, []Message{message}, stream) }()
					select {
					case <-reached:
					case <-time.After(3 * time.Second):
						t.Fatal("image request did not reach the fixture")
					}
					if !deadline {
						cancel()
					}
					<-ctx.Done()
					select {
					case err := <-done:
						require.ErrorIs(t, err, wantErr)
					case <-time.After(500 * time.Millisecond):
						t.Error("cancelled chat stayed blocked in the image request")
						unblock()
						select {
						case drainErr := <-done:
							_ = drainErr
						case <-time.After(3 * time.Second):
							t.Fatal("chat did not exit after fixture release")
						}
					}
					select {
					case <-requestCancelled:
					case <-time.After(500 * time.Millisecond):
						t.Error("image HTTP request did not receive cancellation")
					}
					require.Zero(t, chatCalls.Load(), "cancelled preparation must not reach /api/chat")
					require.Zero(t, laterImages.Load(), "remaining images must not be fetched after cancellation")
				})
			}
		}
	}
}

func TestOllamaImageRequestPreservesSuccessfulInput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			requests := make(chan ollamaapi.ChatRequest, 1)
			c := ollamaImageTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				var req ollamaapi.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				requests <- req
				_, _ = fmt.Fprint(w, `{"message":{"content":"answer"},"done":true}`)
			})
			images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, "remote fixture")
			}))
			defer images.Close()
			message := Message{Role: "user", Images: []string{images.URL}, MultiContent: []api.MessageContentPart{
				{Type: "text", Text: "look"},
				{Type: "image_url", ImageURL: &api.ImageURL{URL: "data:image/png;base64,bG9jYWwgZml4dHVyZQ=="}},
			}}
			require.NoError(t, runOllamaImageChat(t.Context(), c, []Message{message}, stream))
			req := <-requests
			require.Equal(t, stream, *req.Stream)
			require.Len(t, req.Messages, 1)
			require.Equal(t, "look", req.Messages[0].Content)
			wantImgs := []ollamaapi.ImageData{[]byte("remote fixture"), []byte("local fixture")}
			require.Equal(t, wantImgs, req.Messages[0].Images)
		})
	}
}

func TestOllamaChatStreamExitsWithoutDrainOnCancelAndRejectsErrorImages(t *testing.T) {
	t.Run("stream goroutine exits on context cancel without consumer draining", func(t *testing.T) {
		handlerExited := make(chan struct{})
		c := ollamaImageTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			defer close(handlerExited)
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"chunk-1"},"done":false}`)
			flusher.Flush()
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"chunk-2"},"done":false}`)
			flusher.Flush()
			<-r.Context().Done()
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ch, err := c.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil)
		require.NoError(t, err)

		first := <-ch
		require.Equal(t, "chunk-1", first.Content)
		// Consumer stops reading and cancels ctx.
		cancel()

		select {
		case <-handlerExited:
		case <-time.After(1 * time.Second):
			t.Fatal("ollama /api/chat handler stayed alive after context cancel")
		}
		// Once handler exits, OllamaChat.ChatStream goroutine must close ch instead of deadlocking on send.
		closed := make(chan struct{})
		go func() {
			for range ch {
			}
			close(closed)
		}()
		// Wait briefly BEFORE draining? No — test without reading ch by waiting for goroutine exit!
		select {
		case <-closed:
		case <-time.After(1 * time.Second):
			t.Fatal("ChatStream channel never closed")
		}
	})

	t.Run("stream goroutine closes channel when nobody reads after cancel", func(t *testing.T) {
		c := ollamaImageTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","thinking":"step1"},"done":false}`)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"ans"},"done":true}`)
		})

		ctx, cancel := context.WithCancel(t.Context())
		ch, err := c.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil)
		require.NoError(t, err)
		// Cancel immediately WITHOUT reading any item from ch.
		cancel()

		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		// Wait 100ms while nobody reads, then verify ch was already closed without needing a receiver.
		time.Sleep(100 * time.Millisecond)
		select {
		case _, ok := <-ch:
			require.False(t, ok, "expected streamChan to be closed without blocking on unbuffered send")
		case <-timer.C:
			t.Fatal("OllamaChat.ChatStream goroutine leaked blocked on unbuffered streamChan send")
		}
	})

	t.Run("resolveImageForOllama rejects non-2xx HTTP status", func(t *testing.T) {
		requests := make(chan ollamaapi.ChatRequest, 1)
		c := ollamaImageTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			var req ollamaapi.ChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			requests <- req
			_, _ = fmt.Fprint(w, `{"message":{"content":"ok"},"done":true}`)
		})
		notFoundImg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "<html>404 Not Found</html>", http.StatusNotFound)
		}))
		defer notFoundImg.Close()

		msg := Message{Role: "user", Content: "check", Images: []string{notFoundImg.URL}}
		_, err := c.Chat(t.Context(), []Message{msg}, nil)
		require.NoError(t, err)
		req := <-requests
		require.Empty(t, req.Messages[0].Images, "HTTP 404 body must not be forwarded to Ollama as image bytes")
	})

	t.Run("nil config guards return errors instead of panicking", func(t *testing.T) {
		_, err := NewChat(nil, nil)
		require.Error(t, err)
		_, err = NewOllamaChat(nil, nil)
		require.Error(t, err)
	})

	t.Run("default LLM timeout bounds stalled Ollama Chat and ChatStream when ctx has no deadline", func(t *testing.T) {
		prevChat := api.DefaultChatTimeout
		prevStream := api.DefaultStreamTimeout
		api.DefaultChatTimeout = 60 * time.Millisecond
		api.DefaultStreamTimeout = 60 * time.Millisecond
		t.Cleanup(func() {
			api.DefaultChatTimeout = prevChat
			api.DefaultStreamTimeout = prevStream
		})

		unblock := make(chan struct{})
		defer close(unblock)

		c := ollamaImageTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
		})

		start := time.Now()
		_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
		require.Error(t, err)
		require.Less(t, time.Since(start), 2*time.Second)

		start = time.Now()
		err = runOllamaImageChat(context.Background(), c, []Message{{Role: "user", Content: "hi"}}, true)
		require.Error(t, err)
		require.Less(t, time.Since(start), 2*time.Second)
	})
}
