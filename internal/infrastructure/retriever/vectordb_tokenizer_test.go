package retriever_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/repository/retriever/tencentvectordb"
	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/tokenizer"
)

// TestTencentVectorDBJiebaTokenizerChineseBM25 verifies that the upstream
// Tencent VectorDB SDK tokenizer (backed by go-ego/gse + vcaesar/cedar)
// properly segments unspaced Chinese text into multiple word tokens for BM25
// sparse vector encoding, preventing a regression to whitespace-only splitting.
func TestTencentVectorDBJiebaTokenizerChineseBM25(t *testing.T) {
	if err := tencentvectordb.EnsureDefaultStopwords(); err != nil {
		t.Fatalf("EnsureDefaultStopwords failed: %v", err)
	}

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

type airgapRoundTripper struct{}

func (airgapRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errors.New("external network blocked in air-gapped test: " + req.URL.String())
}

// TestTencentVectorDBJiebaTokenizerOfflineNoNetwork verifies that embedded
// stopwords seeding keeps Jieba tokenizer initialization 100% air-gapped with
// zero external HTTP downloads to Tencent COS.
func TestTencentVectorDBJiebaTokenizerOfflineNoNetwork(t *testing.T) {
	if err := tencentvectordb.EnsureDefaultStopwords(); err != nil {
		t.Fatalf("EnsureDefaultStopwords failed: %v", err)
	}

	origTransport := http.DefaultTransport
	http.DefaultTransport = airgapRoundTripper{}
	t.Cleanup(func() { http.DefaultTransport = origTransport })

	jbt, err := tokenizer.NewJiebaTokenizer(&tokenizer.TokenizerParams{
		StopWords: tencentvectordb.StopwordsFilePath(),
	})
	if err != nil {
		t.Fatalf("NewJiebaTokenizer offline failed: %v", err)
	}

	tokens := jbt.Tokenize("企业级知识库支持纯内网离线分词检索")
	if len(tokens) <= 1 {
		t.Fatalf("expected offline Chinese segmentation to produce multiple tokens, got %v", tokens)
	}
}
