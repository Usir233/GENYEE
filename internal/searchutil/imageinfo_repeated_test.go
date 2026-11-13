package searchutil

import (
	"encoding/json"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImageEnrichmentKeepsRepeatedReferencesIndependent(t *testing.T) {
	link := "![diagram](resource://diagram)"
	raw, err := json.Marshal([]types.ImageInfo{{URL: "resource://diagram", Caption: "caption", OCRText: "recognized text"}})
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		enrich    func(string, string) string
		wantImage string
	}{
		{"wiki and questions", EnrichContentWithImageInfo, "<image url=\"resource://diagram\">\n<image_original>" + link + "</image_original>\n<image_caption>caption</image_caption>\n<image_ocr>recognized text</image_ocr>\n</image>"},
		{"summary captions", EnrichContentCaptionOnly, link + "\n<image_caption>caption</image_caption>"},
		{"summary OCR", EnrichContentCaptionAndOCR, link + "\n<image_caption>caption</image_caption>\n<image_ocr>recognized text</image_ocr>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.enrich("first section\n"+link+"\nsecond section\n"+link, string(raw))
			require.Equal(t, "first section\n"+tc.wantImage+"\nsecond section\n"+tc.wantImage, got)
		})
	}
}

func TestImageEnrichmentDoesNotRewriteLinksInsideMetadata(t *testing.T) {
	first := "![first](resource://first)"
	second := "![second](resource://second)"
	raw, err := json.Marshal([]types.ImageInfo{
		{URL: "resource://first", Caption: "See " + second},
		{URL: "resource://second", Caption: "second caption"},
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		enrich func(string, string) string
		want   string
	}{
		{"wiki and questions", EnrichContentWithImageInfo,
			"<image url=\"resource://first\">\n<image_original>" + first + "</image_original>\n<image_caption>See " + second + "</image_caption>\n</image>\n" +
				"<image url=\"resource://second\">\n<image_original>" + second + "</image_original>\n<image_caption>second caption</image_caption>\n</image>"},
		{"summary captions", EnrichContentCaptionOnly, first + "\n<image_caption>See " + second + "</image_caption>\n" + second + "\n<image_caption>second caption</image_caption>"},
		{"summary OCR", EnrichContentCaptionAndOCR, first + "\n<image_caption>See " + second + "</image_caption>\n" + second + "\n<image_caption>second caption</image_caption>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.enrich(first+"\n"+second, string(raw)))
		})
	}
}
