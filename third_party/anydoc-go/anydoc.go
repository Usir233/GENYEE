// Package anydoc converts documents (Word, PowerPoint, Excel, OpenDocument,
// RTF, EPUB, CSV, and PDF) to GitHub-Flavored Markdown, with full access to
// the parsed document model and embedded assets.
//
// This is the Go binding. It links a Rust static library through cgo; the
// module packages archives for every supported platform. See the README for
// the supported platform matrix and musl build tag.
package anydoc

/*
#cgo CFLAGS: -I${SRCDIR}/include
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/lib/darwin_arm64 -lanydoc_go -lm -lstdc++
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/darwin_amd64 -lanydoc_go -lm -lstdc++
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/lib/windows_amd64 -lanydoc_go

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "include/anydoc.h"

typedef struct {
	char *ptr;
	uintptr_t len;
} anydoc_str_result;

typedef struct {
	uint8_t *ptr;
	uintptr_t len;
} anydoc_buf_result;

static inline int anydoc_go_to_markdown(const char *path, anydoc_str_result *res) {
	return anydoc_to_markdown(path, &res->ptr, &res->len);
}

static inline int anydoc_go_to_markdown_bytes(
	const uint8_t *bytes, uintptr_t len, int format_tag, anydoc_str_result *res
) {
	return anydoc_to_markdown_bytes(bytes, len, format_tag, &res->ptr, &res->len);
}

static inline int anydoc_go_to_markdown_with_asset_links(
	const uint8_t *bytes, uintptr_t len, int format_tag, anydoc_str_result *res
) {
	return anydoc_to_markdown_with_asset_links(bytes, len, format_tag, &res->ptr, &res->len);
}

static inline int anydoc_go_to_document(
	const uint8_t *bytes, uintptr_t len, int format_tag, anydoc_buf_result *res
) {
	return anydoc_to_document(bytes, len, format_tag, &res->ptr, &res->len);
}

static inline int anydoc_go_to_document_with_asset_links(
	const uint8_t *bytes, uintptr_t len, int format_tag, anydoc_buf_result *res
) {
	return anydoc_to_document_with_asset_links(bytes, len, format_tag, &res->ptr, &res->len);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// call runs one ABI entry point and turns its status code into an error.
//
// The goroutine is pinned for the duration because the ABI reports the error
// message through a thread-local slot that a *second* call
// (`anydoc_last_error`) reads. Go is free to resume a goroutine on a different
// OS thread after a cgo call returns, and a conversion is long enough for that
// to happen: the message would then be missing, or belong to another
// conversion that ran on the thread we landed on.
func call(entry func() C.int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if code := entry(); code != errOK {
		return convertError(code)
	}
	return nil
}

func validateCPath(path string) error {
	if path == "" {
		return &ConvertError{Kind: "invalid_argument", Detail: "empty path"}
	}
	if strings.IndexByte(path, 0) >= 0 {
		return &ConvertError{Kind: "invalid_argument", Detail: "path contains NUL byte"}
	}
	if !utf8.ValidString(path) {
		return &ConvertError{Kind: "invalid_argument", Detail: "path is not valid UTF-8"}
	}
	return nil
}

func resolveFormatTag(format *Format) (C.int, error) {
	if format == nil {
		return C.int(C.ANYDOC_FORMAT_NONE), nil
	}
	tag := formatToTag(*format)
	if tag == C.int(C.ANYDOC_FORMAT_NONE) {
		return 0, &ConvertError{Kind: "unknown_format", Detail: "unknown format: " + string(*format)}
	}
	return tag, nil
}

func allocStrResult() (*C.anydoc_str_result, error) {
	res := (*C.anydoc_str_result)(C.calloc(1, C.size_t(unsafe.Sizeof(C.anydoc_str_result{}))))
	if res == nil {
		return nil, &ConvertError{Kind: "io", Detail: "out of memory allocating FFI result slot"}
	}
	return res, nil
}

func extractAndFreeStrResult(res *C.anydoc_str_result) (string, error) {
	defer func() {
		if res.ptr != nil {
			C.anydoc_string_free(res.ptr)
			res.ptr = nil
		}
		C.free(unsafe.Pointer(res))
	}()
	return cStringN(res.ptr, res.len)
}

// ToMarkdown converts a document file to Markdown. The format is detected
// from the file content; the extension is the fallback for signature-less
// formats (CSV) and unrecognizable containers.
func ToMarkdown(path string) (string, error) {
	if err := validateCPath(path); err != nil {
		return "", err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	res, err := allocStrResult()
	if err != nil {
		return "", err
	}
	if err := call(func() C.int { return C.anydoc_go_to_markdown(cpath, res) }); err != nil {
		C.free(unsafe.Pointer(res))
		return "", err
	}
	return extractAndFreeStrResult(res)
}

// ToMarkdownBytes converts an in-memory document to Markdown. Pass a Format
// to select the parser, or nil to detect it from the content, which
// signature-less formats (CSV) have to name explicitly.
func ToMarkdownBytes(data []byte, format *Format) (string, error) {
	if len(data) == 0 {
		return "", &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag, err := resolveFormatTag(format)
	if err != nil {
		return "", err
	}
	res, err := allocStrResult()
	if err != nil {
		return "", err
	}
	callErr := call(func() C.int {
		return C.anydoc_go_to_markdown_bytes(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, res,
		)
	})
	runtime.KeepAlive(data)
	if callErr != nil {
		C.free(unsafe.Pointer(res))
		return "", callErr
	}
	return extractAndFreeStrResult(res)
}

// ToMarkdownWithAssetLinks converts an in-memory document to Markdown with
// embedded images rewritten as `![alt](images/image-N.ext)` so they keep
// their original positions. The Markdown itself is produced by anydoc's
// official GFM serializer.
func ToMarkdownWithAssetLinks(data []byte, format *Format) (string, error) {
	if len(data) == 0 {
		return "", &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag, err := resolveFormatTag(format)
	if err != nil {
		return "", err
	}
	res, err := allocStrResult()
	if err != nil {
		return "", err
	}
	callErr := call(func() C.int {
		return C.anydoc_go_to_markdown_with_asset_links(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, res,
		)
	})
	runtime.KeepAlive(data)
	if callErr != nil {
		C.free(unsafe.Pointer(res))
		return "", callErr
	}
	return extractAndFreeStrResult(res)
}

func allocBufResult() (*C.anydoc_buf_result, error) {
	res := (*C.anydoc_buf_result)(C.calloc(1, C.size_t(unsafe.Sizeof(C.anydoc_buf_result{}))))
	if res == nil {
		return nil, &ConvertError{Kind: "io", Detail: "out of memory allocating FFI buffer slot"}
	}
	return res, nil
}

func extractAndFreeBufResult(res *C.anydoc_buf_result) ([]byte, bool, error) {
	defer func() {
		if res.ptr != nil {
			C.anydoc_buffer_free(res.ptr, res.len)
			res.ptr = nil
		}
		C.free(unsafe.Pointer(res))
	}()
	if res.ptr == nil || res.len == 0 {
		return nil, true, nil
	}
	raw, err := cGoBytes(unsafe.Pointer(res.ptr), res.len)
	return raw, false, err
}

// ToDocument parses an in-memory document into the document model, which also
// carries the embedded assets. Pass a Format to select the parser, or nil to
// detect it from the content.
//
// Unsupported for PDF: PDF conversion produces Markdown directly and has no
// document-model form; use ToMarkdownBytes.
func ToDocument(data []byte, format *Format) (*Document, error) {
	if len(data) == 0 {
		return nil, &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag, err := resolveFormatTag(format)
	if err != nil {
		return nil, err
	}
	res, err := allocBufResult()
	if err != nil {
		return nil, err
	}
	callErr := call(func() C.int {
		return C.anydoc_go_to_document(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, res,
		)
	})
	runtime.KeepAlive(data)
	if callErr != nil {
		C.free(unsafe.Pointer(res))
		return nil, callErr
	}
	// Copy the C buffer into a Go-owned slice and release the Rust allocation
	// immediately before decoding the AST, minimizing peak concurrent memory.
	raw, empty, err := extractAndFreeBufResult(res)
	if err != nil {
		return nil, err
	}
	if empty {
		return &Document{}, nil
	}
	return decodeDocument(raw)
}

// ToDocumentWithAssetLinks parses once and returns both the original model
// (including asset IDs and bytes) and Markdown with embedded image links.
// PDF has no document model; use ToMarkdownBytes for it.
func ToDocumentWithAssetLinks(data []byte, format *Format) (*Document, string, error) {
	if len(data) == 0 {
		return nil, "", &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag, err := resolveFormatTag(format)
	if err != nil {
		return nil, "", err
	}
	res, err := allocBufResult()
	if err != nil {
		return nil, "", err
	}
	callErr := call(func() C.int {
		return C.anydoc_go_to_document_with_asset_links(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, res,
		)
	})
	runtime.KeepAlive(data)
	if callErr != nil {
		C.free(unsafe.Pointer(res))
		return nil, "", callErr
	}
	raw, empty, err := extractAndFreeBufResult(res)
	if err != nil {
		return nil, "", err
	}
	if empty {
		return nil, "", &ConvertError{Kind: "malformed", Detail: "empty FFI document buffer"}
	}
	d := &decoder{buf: raw}
	document, err := d.document()
	if err != nil {
		return nil, "", err
	}
	markdown, err := d.str()
	if err != nil {
		return nil, "", err
	}
	return document, markdown, nil
}

// cStringN reads a length-prefixed C buffer as a Go string. The bytes are
// copied, so the caller may free the C buffer immediately after.
func cStringN(s *C.char, n C.uintptr_t) (string, error) {
	if s == nil || n == 0 {
		return "", nil
	}
	b, err := cGoBytes(unsafe.Pointer(s), n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cGoBytes(ptr unsafe.Pointer, n C.uintptr_t) ([]byte, error) {
	if n == 0 {
		return nil, nil
	}
	if ptr == nil {
		return nil, &ConvertError{Kind: "malformed", Detail: "null FFI output buffer"}
	}
	// C.GoBytes accepts a C int length even on 64-bit Go builds.
	if uint64(n) > 1<<31-1 {
		return nil, &ConvertError{
			Kind:   "resource_limit",
			Detail: fmt.Sprintf("FFI output is too large for Go: %d bytes", n),
		}
	}
	return C.GoBytes(ptr, C.int(n)), nil
}

// cString reads a NUL-terminated C string as a Go string.
func cString(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}
