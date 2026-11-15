package embedding

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/panjf2000/ants/v2"
)

type batchEmbedder struct {
	pool       *ants.Pool
	submitSlot chan struct{}
}

func NewBatchEmbedder(pool *ants.Pool) EmbedderPooler {
	return &batchEmbedder{pool: pool, submitSlot: make(chan struct{}, 1)}
}

func (e *batchEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The caller may reuse its input slice as soon as cancellation returns,
	// while a pool submission is still unwinding in the background.
	texts = append([]string(nil), texts...)
	type result struct {
		vectors [][]float32
		err     error
	}
	done := make(chan result, 1)
	go func() {
		vectors, err := e.batchEmbed(ctx, model, texts)
		done <- result{vectors: vectors, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result.vectors, result.err
	}
}

// ants.Submit cannot be interrupted while another request holds the pool.
// Keep that wait off the caller, with at most one Submit waiter per pooler.
// Other calls wait for submitSlot with their own context; the accepted callback
// also checks cancellation before calling the provider.
func (e *batchEmbedder) submit(ctx context.Context, task func()) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case e.submitSlot <- struct{}{}:
	}
	defer func() { <-e.submitSlot }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.pool.Submit(task)
}

func (e *batchEmbedder) batchEmbed(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Create goroutine pool for concurrent processing of document chunks
	var wg sync.WaitGroup
	var mu sync.Mutex  // For synchronizing access to error
	var firstErr error // Record the first error that occurs
	batchSizeStr := os.Getenv("BATCH_EMBED_SIZE")
	if batchSizeStr == "" {
		batchSizeStr = "5"
	}
	batchSize, err := strconv.Atoi(batchSizeStr)
	if err != nil {
		return nil, err
	}
	if batchSize <= 0 {
		return nil, fmt.Errorf("BATCH_EMBED_SIZE must be greater than 0")
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	results := make([][]float32, len(texts))

	// Function to process each document chunk
	processChunk := func(start int, chunk []string) func() {
		return func() {
			defer wg.Done()
			// If an error has already occurred, don't continue processing. The
			// read takes mu for the same reason the writes below do: every pool
			// worker evaluates this guard, so an unsynchronised read races with
			// another worker's write. Beyond the detector, a worker that misses
			// the write bills another provider request after the run has already
			// failed, which is exactly what this guard exists to prevent.
			mu.Lock()
			aborted := firstErr != nil
			mu.Unlock()
			if aborted || ctx.Err() != nil {
				return
			}
			// Embed text
			embedding, err := model.BatchEmbed(ctx, chunk)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if len(embedding) != len(chunk) {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("embedding model returned %d embeddings for %d inputs", len(embedding), len(chunk))
				}
				mu.Unlock()
				return
			}
			copy(results[start:start+len(chunk)], embedding)
		}
	}

	// Submit tasks to the goroutine pool batch by batch without upfront per-item allocation
	for start := 0; start < len(texts); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + min(batchSize, len(texts)-start)
		chunk := texts[start:end]
		wg.Add(1)
		if err := e.submit(ctx, processChunk(start, chunk)); err != nil {
			wg.Done() // Submit did not accept this callback.
			return nil, err
		}
		start = end
	}

	// Wait for all tasks to complete
	wg.Wait()

	// Check if any errors occurred
	if firstErr != nil {
		return nil, firstErr
	}

	return results, nil
}
