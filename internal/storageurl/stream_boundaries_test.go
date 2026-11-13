package storageurl

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// SSE deltas can split anywhere in a link, including inside its URI scheme.
// The accumulated stream must agree with rewriting the completed answer.
func TestStreamRewriterEveryReferenceBoundary(t *testing.T) {
	for _, ref := range []string{
		"resource://AbCdEfGhIjKlMnOpQrStUv",
		"local://42/report.pdf",
		"minio://bucket/42/report.pdf",
		"s3://bucket/42/report.pdf",
		"cos://bucket/42/report.pdf",
		"tos://bucket/42/report.pdf",
		"oss://bucket/42/report.pdf",
		"obs://bucket/42/report.pdf",
		"ks3://bucket/42/report.pdf",
		"storage://backend-1/s3://bucket/42/report.pdf",
	} {
		for _, wrap := range []string{"Download %s now", "[report](%s)", "![preview](%s)", `<a href="%s">report</a>`} {
			text := fmt.Sprintf(wrap, ref)
			want := strings.Replace(text, ref, "https://cdn.example.com/report.pdf", 1)
			for split := 1; split < len(text); split++ {
				t.Run(fmt.Sprintf("%s/%s/%d", ref, wrap, split), func(t *testing.T) {
					sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/report.pdf"), "TEST"))
					ctx := context.Background()
					got := sr.Push(ctx, "answer", text[:split], false, nil)
					got += sr.Push(ctx, "answer", text[split:], true, nil)
					require.Equal(t, want, got)
					require.Empty(t, sr.FlushAll(ctx))
				})
			}
		}
	}
}
