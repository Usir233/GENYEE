package utils

import "testing"

func TestSafeContentTypeByFilename_BlocksActiveBrowserContentAndDirectoryDots(t *testing.T) {
	activeExts := []string{
		"payload.svg",
		"payload.html",
		"payload.xhtml",
		"payload.shtml",
		"payload.mht",
		"payload.mhtml",
		"payload.xsl",
		"payload.xslt",
		"payload.rdf",
		"payload.mathml",
		"payload.swf",
		"payload.svg.",
		"payload.html ",
		"local://42/dir.png/payload.svg.",
	}
	for _, name := range activeExts {
		ct, inline := SafeContentTypeByFilename(name)
		if inline {
			t.Errorf("SafeContentTypeByFilename(%q) allowed inline display", name)
		}
		if ct != "application/octet-stream" {
			t.Errorf("SafeContentTypeByFilename(%q) content-type = %q, want application/octet-stream", name, ct)
		}
	}

	// Dots in directory segments must not spoof the basename extension.
	for _, pathWithDirDot := range []string{
		"local://42/dir.png/payload",
		"minio://tenant-1/folder.pdf/README",
		`C:\uploads\dir.jpg\noext`,
	} {
		ct, inline := SafeContentTypeByFilename(pathWithDirDot)
		if ct != "application/octet-stream" {
			t.Errorf("SafeContentTypeByFilename(%q) misclassified directory dot as %q (inline=%v)", pathWithDirDot, ct, inline)
		}
	}

	// Legitimate safe basename extensions still work across paths.
	if ct, inline := SafeContentTypeByFilename("local://42/dir.html/report.pdf"); ct != "application/pdf" || !inline {
		t.Errorf("SafeContentTypeByFilename(report.pdf) = (%q, %v), want (application/pdf, true)", ct, inline)
	}
}
