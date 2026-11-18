package docparser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// defaultJSONChunkSize is the target chunk size in bytes for JSON semantic
// splitting. Approximates 512 tokens (1 token ≈ 3-4 bytes for mixed content).
const (
	defaultJSONChunkSize = 1536
	maxJSONSplitDepth    = 32
	maxJSONMarkdownBytes = maxZipEntryBytes
)

// minJSONChunkSize is the minimum chunk size. A new chunk is only started
// when the current chunk has reached at least this size.
var minJSONChunkSize = defaultJSONChunkSize - 200

// Errors returned by ValidateJSONContent and jsonToMarkdownContext.
var (
	ErrEmptyJSONContent     = errors.New("empty JSON content")
	ErrInvalidJSONContent   = errors.New("invalid JSON content")
	ErrJSONMarkdownTooLarge = errors.New("json markdown expansion exceeds limit")
)

// ValidateJSONContent reports whether data is well-formed JSON after stripping
// a leading UTF-8 BOM. Upload paths call this before enqueueing parse work so
// JSONC / truncated payloads fail immediately instead of after async retries.
func ValidateJSONContent(data []byte) error {
	data = trimBOM(data)
	if len(data) == 0 {
		return ErrEmptyJSONContent
	}
	if !json.Valid(data) {
		return ErrInvalidJSONContent
	}
	return nil
}

// jsonToMarkdown converts raw JSON bytes into markdown text
//
// Key properties:
//   - Every output chunk is a **valid JSON object** (not a fragment).
//   - Nested paths from root to leaf are **fully preserved** in each chunk.
//   - Arrays are converted to index-keyed dicts so the algorithm is uniform.
//   - Small objects that fit within maxChunkSize are kept intact (not split).
//   - The output is a series of fenced ```json code blocks separated by \n\n,
//     which the downstream text chunker can split at block boundaries.
func jsonToMarkdown(data []byte) (string, error) {
	return jsonToMarkdownContext(context.Background(), data, maxJSONMarkdownBytes)
}

func jsonToMarkdownContext(ctx context.Context, data []byte, limit int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := ValidateJSONContent(data); err != nil {
		return "", err
	}
	data = trimBOM(data)

	// UseNumber keeps each number as written. Decoded into a float64, an id
	// above 2^53 changed digits: 12345678901234567890 came out as
	// 12345678901234567000. ValidateJSONContent above has already rejected
	// anything after the value.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var parsed interface{}
	if err := decoder.Decode(&parsed); err != nil {
		return "", fmt.Errorf("failed to parse JSON: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Normalize: convert top-level arrays to index-keyed dicts
	normalized := listToDictPreprocess(parsed)

	// If the whole thing fits in one chunk, just format it
	wholeSize := jsonSize(normalized)
	if wholeSize <= defaultJSONChunkSize {
		formatted := wrapCodeBlock(formatValue(normalized))
		if limit > 0 && len(formatted) > limit {
			return "", ErrJSONMarkdownTooLarge
		}
		return formatted, nil
	}

	// Recursive split
	chunks, err := recursiveJSONSplitCtx(ctx, normalized, nil, nil)
	if err != nil {
		return "", err
	}

	// Convert each chunk dict to a fenced code block while enforcing the output cap.
	blocks := make([]string, 0, len(chunks))
	totalBytes := 0
	for _, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(chunk) == 0 {
			continue
		}
		block := wrapCodeBlock(formatValue(chunk))
		if len(blocks) > 0 {
			totalBytes += 2
		}
		totalBytes += len(block)
		if limit > 0 && totalBytes > limit {
			return "", ErrJSONMarkdownTooLarge
		}
		blocks = append(blocks, block)
	}

	if len(blocks) == 0 {
		out := wrapCodeBlock(formatValue(normalized))
		if limit > 0 && len(out) > limit {
			return "", ErrJSONMarkdownTooLarge
		}
		return out, nil
	}
	return strings.Join(blocks, "\n\n"), nil
}

// ---------------------------------------------------------------------------
// RecursiveJsonSplitter core algorithm
// ---------------------------------------------------------------------------

func jsonPathOverhead(path []string) int {
	if len(path) == 0 {
		return 0
	}
	probe := map[string]interface{}{}
	setNestedDict(probe, path, "")
	if sz := jsonSize(probe); sz > 2 {
		return sz - 2
	}
	return 0
}

// recursiveJSONSplitCtx splits a JSON dict into a list of JSON dicts,
// each fitting within defaultJSONChunkSize while honoring context cancellation.
func recursiveJSONSplitCtx(
	ctx context.Context,
	data interface{},
	currentPath []string,
	chunks []map[string]interface{},
) ([]map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if chunks == nil {
		chunks = []map[string]interface{}{{}}
	}

	dict, ok := data.(map[string]interface{})
	if !ok || len(currentPath) >= maxJSONSplitDepth {
		// Scalar, already-processed value, or deep wrapper limit reached:
		// keep subtree intact rather than repeating deep ancestor chains per leaf.
		if len(currentPath) > 0 && len(chunks) > 0 {
			setNestedDict(chunks[len(chunks)-1], currentPath, data)
		}
		return chunks, nil
	}

	// Sort keys for deterministic output
	keys := sortedKeys(dict)
	pathOverhead := jsonPathOverhead(currentPath)

	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value := dict[key]
		newPath := append(append([]string{}, currentPath...), key)

		// Measure payload size net of ancestor path wrappers so deep wrapper keys
		// never force 1 scalar item per chunk.
		rawChunkSize := jsonSize(chunks[len(chunks)-1])
		chunkPayload := rawChunkSize
		if rawChunkSize > 2 && pathOverhead > 0 {
			if rawChunkSize > pathOverhead {
				chunkPayload = rawChunkSize - pathOverhead
			} else {
				chunkPayload = 0
			}
		}
		itemSize := jsonSize(map[string]interface{}{key: value})
		remaining := defaultJSONChunkSize - chunkPayload

		if itemSize <= remaining {
			// Item fits in the current chunk — add it preserving the path
			setNestedDict(chunks[len(chunks)-1], newPath, value)
		} else {
			// Item doesn't fit
			if chunkPayload >= minJSONChunkSize {
				// Current chunk is big enough, start a new one
				chunks = append(chunks, map[string]interface{}{})
			}

			// Check if the value itself is a dict/list that can be recursed into
			normalized := listToDictPreprocess(value)
			subDict, isDict := normalized.(map[string]interface{})
			if isDict && len(newPath) < maxJSONSplitDepth && canSplitDict(subDict) {
				// Recurse into the sub-object
				var err error
				chunks, err = recursiveJSONSplitCtx(ctx, subDict, newPath, chunks)
				if err != nil {
					return nil, err
				}
			} else {
				// Cannot split further (scalar, single-key dict, or max depth) — place as-is
				setNestedDict(chunks[len(chunks)-1], newPath, value)
			}
		}
	}

	return chunks, nil
}

// setNestedDict sets a value in a nested dict structure, creating
// intermediate dicts as needed. This preserves the full JSON path.
//
// Example: setNestedDict(d, ["config","db","host"], "localhost")
// produces: {"config": {"db": {"host": "localhost"}}}
func setNestedDict(d map[string]interface{}, path []string, value interface{}) {
	if len(path) == 0 {
		return
	}
	current := d
	for _, key := range path[:len(path)-1] {
		next, ok := current[key]
		if !ok {
			next = map[string]interface{}{}
			current[key] = next
		}
		if nextDict, ok := next.(map[string]interface{}); ok {
			current = nextDict
		} else {
			// Path conflict (existing value is not a dict) — overwrite
			newDict := map[string]interface{}{}
			current[key] = newDict
			current = newDict
		}
	}
	current[path[len(path)-1]] = value
}

// listToDictPreprocess recursively converts JSON arrays to index-keyed
// dicts so the splitter can treat everything uniformly.
//
// Example: ["a","b","c"] → {"0":"a", "1":"b", "2":"c"}
func listToDictPreprocess(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for k, val := range v {
			result[k] = listToDictPreprocess(val)
		}
		return result
	case []interface{}:
		result := make(map[string]interface{}, len(v))
		for i, item := range v {
			result[fmt.Sprintf("%d", i)] = listToDictPreprocess(item)
		}
		return result
	default:
		return data
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// jsonSize returns the serialized JSON byte length of a value.
func jsonSize(v interface{}) int {
	b, err := marshalJSON(v, "")
	if err != nil {
		return 0
	}
	return len(b)
}

// formatValue formats a JSON value with indentation.
func formatValue(v interface{}) string {
	b, err := marshalJSON(v, "  ")
	if err != nil {
		b, _ = marshalJSON(v, "")
	}
	return string(b)
}

// marshalJSON encodes v like json.Marshal, indented when indent is set, but
// leaves <, > and & as they are. json.Marshal writes them as \u003c, \u003e
// and \u0026, which is meant for embedding in HTML and only garbles the text
// a chunk is read and searched as.
func marshalJSON(v interface{}, indent string) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// wrapCodeBlock wraps content in a fenced JSON code block.
func wrapCodeBlock(content string) string {
	return "```json\n" + content + "\n```"
}

// trimBOM removes a UTF-8 BOM prefix if present.
func trimBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// canSplitDict returns true if a dict can be meaningfully split.
// A dict with multiple keys can be split by distributing keys across chunks.
// A dict with a single key can still be split if its value is a splittable dict.
func canSplitDict(d map[string]interface{}) bool {
	// Single-key containers do not prevent splitting a deeper multi-key object.
	// Walk iteratively so deeply wrapped exports do not add recursion here.
	for len(d) == 1 {
		for _, value := range d {
			sub, ok := value.(map[string]interface{})
			if !ok {
				return false
			}
			d = sub
		}
	}
	return len(d) > 1
}

// sortedKeys returns the keys of a map in sorted order.
// When all keys are numeric strings (from array-to-dict conversion),
// sorts numerically so "2" comes before "10".
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	allNumeric := true
	for k := range m {
		keys = append(keys, k)
		if allNumeric {
			if _, err := strconv.Atoi(k); err != nil {
				allNumeric = false
			}
		}
	}
	if allNumeric {
		sort.Slice(keys, func(i, j int) bool {
			ni, _ := strconv.Atoi(keys[i])
			nj, _ := strconv.Atoi(keys[j])
			return ni < nj
		})
	} else {
		sort.Strings(keys)
	}
	return keys
}
