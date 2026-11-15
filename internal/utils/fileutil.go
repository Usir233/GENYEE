package utils

import "strings"

func normalizeFileExt(ext string) string {
	ext = strings.TrimRight(strings.TrimSpace(ext), " .")
	return strings.ToLower(ext)
}

// IsActiveBrowserContentExt reports whether a file extension can execute script
// or otherwise participate as active same-origin browser content when served
// inline. User-uploaded files with these extensions must be downloaded instead
// of previewed inline.
func IsActiveBrowserContentExt(ext string) bool {
	switch normalizeFileExt(ext) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".shtml",
		".mht", ".mhtml", ".xml", ".xsl", ".xslt", ".rdf", ".mathml",
		".js", ".mjs", ".css", ".swf":
		return true
	default:
		return false
	}
}

// GetContentTypeByExt returns the content type based on file extension
func GetContentTypeByExt(ext string) string {
	ext = normalizeFileExt(ext)
	if IsActiveBrowserContentExt(ext) {
		return "application/octet-stream"
	}
	switch ext {
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	case ".md":
		return "text/markdown; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// SafeContentTypeByFilename returns a browser-safe content type for serving
// user-controlled files and whether that file may be displayed inline.
func SafeContentTypeByFilename(filename string) (string, bool) {
	base := filename
	if slash := strings.LastIndexAny(base, `/\`); slash >= 0 {
		base = base[slash+1:]
	}
	base = strings.TrimRight(strings.TrimSpace(base), " .")
	var ext string
	if idx := strings.LastIndex(base, "."); idx >= 0 {
		ext = base[idx:]
	}
	if IsActiveBrowserContentExt(ext) {
		return "application/octet-stream", false
	}
	return GetContentTypeByExt(ext), true
}
