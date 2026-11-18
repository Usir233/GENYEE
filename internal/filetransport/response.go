// Package filetransport streams already-authorized files. It performs no
// resource lookup or permission inference; callers own those boundaries.
package filetransport

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// Options defines response metadata for an already-authorized reader.
type Options struct {
	Filename     string
	Download     bool
	ContentType  string
	Disposition  string
	CacheControl string
	Size         int64
}

func safeFilenameBase(raw string) string {
	base := strings.TrimSpace(raw)
	if slash := strings.LastIndexAny(base, `/\`); slash >= 0 {
		base = base[slash+1:]
	}
	var cleaned strings.Builder
	cleaned.Grow(len(base))
	for _, r := range base {
		if r >= 0x20 && r != 0x7f {
			cleaned.WriteRune(r)
		}
	}
	out := strings.TrimSpace(cleaned.String())
	if out == "" || out == "." || out == ".." {
		return ""
	}
	return out
}

func isActiveBrowserMediaType(raw string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(raw, ";")[0]))
	} else {
		mediaType = strings.ToLower(mediaType)
	}
	switch mediaType {
	case "text/html", "application/xhtml+xml", "image/svg+xml",
		"text/xml", "application/xml", "text/xsl", "application/xslt+xml",
		"application/rdf+xml", "application/mathml+xml",
		"text/javascript", "application/javascript", "application/x-javascript",
		"text/ecmascript", "application/ecmascript",
		"message/rfc822", "multipart/related", "application/x-shockwave-flash":
		return true
	default:
		return false
	}
}

// Serve closes reader on every path. Seekable backends support standard Range
// and HEAD via ServeContent; streaming-only backends return the full response
// without buffering the object just to implement seeking (RFC 9110 section 14.2).
func Serve(w http.ResponseWriter, r *http.Request, reader io.ReadCloser, options Options) error {
	defer func() { _ = reader.Close() }()
	safeBase := safeFilenameBase(options.Filename)
	contentType, inline := secutils.SafeContentTypeByFilename(options.Filename)
	if options.Download {
		inline = false
	}
	if strings.TrimSpace(options.ContentType) != "" {
		contentType = strings.TrimSpace(options.ContentType)
		if !options.Download && isActiveBrowserMediaType(contentType) {
			contentType = "application/octet-stream"
			inline = false
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	switch {
	case options.Disposition != "":
		w.Header().Set("Content-Disposition", options.Disposition)
	case safeBase != "":
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType(disposition,
				map[string]string{"filename": safeBase}))
	default:
		w.Header().Set("Content-Disposition", disposition)
	}
	if options.CacheControl != "" {
		w.Header().Set("Cache-Control", options.CacheControl)
	}
	if seeker, ok := reader.(io.ReadSeeker); ok {
		http.ServeContent(w, r, safeBase, time.Time{}, seeker)
		return nil
	}
	w.Header().Set("Accept-Ranges", "none")
	if options.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(options.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return nil
	}
	_, err := io.Copy(w, reader)
	return err
}
