package docparser

import (
	"encoding/json"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPythonImageFilenameReferenceContract(t *testing.T) {
	// The Python public-parser tests verify these same emitted references. Keep
	// URI encoding intact when matching original_ref, including literal %20.
	data, err := os.ReadFile("../../../docreader/tests/fixtures/image_filename_refs.json")
	require.NoError(t, err)
	var cases []struct {
		Filename        string `json:"filename"`
		ImageRef        string `json:"image_ref"`
		PDFPageRef      string `json:"pdf_page_ref"`
		ImageMarkdown   string `json:"image_markdown"`
		PDFPageMarkdown string `json:"pdf_page_markdown"`
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	for _, tc := range cases {
		for _, image := range []struct{ kind, ref, markdown string }{
			{"image", tc.ImageRef, tc.ImageMarkdown},
			{"pdf", tc.PDFPageRef, tc.PDFPageMarkdown},
		} {
			t.Run(image.kind+"/"+tc.Filename, func(t *testing.T) {
				storage := &captureSaveBytes{}
				result := &types.ReadResult{
					MarkdownContent: image.markdown,
					ImageRefs: []types.ImageRef{{
						Filename: path.Base(image.ref), OriginalRef: image.ref,
						MimeType: "image/png", ImageData: createTestPNG(200, 150),
					}},
				}
				out, images, err := NewImageResolver().ResolveAndStore(t.Context(), result, storage, 1)
				require.NoError(t, err)
				require.Len(t, storage.saved, 1)
				require.Len(t, images, 1)
				require.Equal(t, image.ref, images[0].OriginalRef)
				require.Equal(t, strings.Replace(image.markdown, image.ref, images[0].ServingURL, 1), out)
			})
		}
	}
}
