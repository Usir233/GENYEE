package retriever_test

import (
	"slices"
	"testing"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/tokenizer"
)

// TestTencentVectorDBJiebaTokenizerChineseBM25 verifies that the upstream
// Tencent VectorDB SDK tokenizer (backed by go-ego/gse + vcaesar/cedar)
// properly segments unspaced Chinese text into multiple word tokens for BM25
// sparse vector encoding, preventing a regression to whitespace-only splitting.
func TestTencentVectorDBJiebaTokenizerChineseBM25(t *testing.T) {
	jbt, err := tokenizer.NewJiebaTokenizer(&tokenizer.TokenizerParams{
		StopWords: false,
	})
	if err != nil {
		t.Fatalf("NewJiebaTokenizer failed: %v", err)
	}

	const sentence = "腾讯开源的知识库检索系统支持混合搜索"
	tokens := jbt.Tokenize(sentence)
	if len(tokens) <= 1 {
		t.Fatalf("expected Chinese sentence %q to be segmented into multiple tokens, got %d: %v", sentence, len(tokens), tokens)
	}
	if len(tokens) == 1 && tokens[0] == sentence {
		t.Fatalf("tokenizer fell back to whitespace splitting without Chinese word segmentation: %v", tokens)
	}

	for _, want := range []string{"检索", "混合", "搜索"} {
		if !slices.Contains(tokens, want) {
			t.Errorf("expected segmented tokens %v to contain %q", tokens, want)
		}
	}

	encoded := jbt.Encode(sentence)
	if len(encoded) <= 1 {
		t.Fatalf("expected Encode(%q) to return multiple token hashes, got %d: %v", sentence, len(encoded), encoded)
	}
}
