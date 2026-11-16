package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/docreader/proto"
	"github.com/Usir233/GENYEE/internal/types"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func buildSpoofedDimensionsPNG(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})

	writeChunk := func(chunkType string, data []byte) {
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(data)))
		buf.WriteString(chunkType)
		buf.Write(data)
		crc := crc32.NewIEEE()
		_, _ = crc.Write([]byte(chunkType))
		_, _ = crc.Write(data)
		_ = binary.Write(&buf, binary.BigEndian, crc.Sum32())
	}

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8  // bit depth
	ihdr[9] = 6  // RGBA
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace
	writeChunk("IHDR", ihdr)
	writeChunk("IDAT", []byte{0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01})
	writeChunk("IEND", nil)
	return buf.Bytes()
}

func TestAuditW7_JSONToMarkdown_DeepPathAmplificationAndContextCancel(t *testing.T) {
	// 1. Cancelled context must stop JSON conversion promptly.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&SimpleFormatReader{}).Read(cancelCtx, &types.ReadRequest{
		FileContent: []byte(`{"alpha":1,"beta":2}`),
		FileType:    "json",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from SimpleFormatReader JSON Read, got %v", err)
	}

	// 2. Deep wrapper path around a 200-element array (~6 KiB input) must NOT amplify
	// into >10 MiB of repeated indented ancestor chains.
	var sb strings.Builder
	const depth = 150
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&sb, `{"wrapper_key_%03d":`, i)
	}
	sb.WriteByte('[')
	for i := 0; i < 200; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", i)
	}
	sb.WriteByte(']')
	for i := 0; i < depth; i++ {
		sb.WriteByte('}')
	}

	md, err := jsonToMarkdown([]byte(sb.String()))
	if err != nil {
		t.Fatalf("jsonToMarkdown unexpected error: %v", err)
	}
	if len(md) > 256*1024 {
		t.Fatalf("deep JSON path amplified %dB input into %dB markdown (want <= 262144B)", sb.Len(), len(md))
	}
}

func TestAuditW7_MarkdownImageScanner_LinearTimeOnUnclosedImageSyntax(t *testing.T) {
	cases := []struct {
		name string
		md   string
	}{
		{"unclosed_alt", strings.Repeat("![x", 35000)},
		{"unclosed_paren", strings.Repeat("![a](", 30000)},
		{"unclosed_angle", strings.Repeat("![a](<x ", 25000)},
	}
	for _, tc := range cases {
		start := time.Now()
		got := StripMarkdownImages(tc.md)
		elapsed := time.Since(start)
		if got != tc.md {
			t.Fatalf("%s: expected unchanged unclosed markdown", tc.name)
		}
		if elapsed > 250*time.Millisecond {
			t.Fatalf("%s: StripMarkdownImages took %v on %d bytes (quadratic regression)", tc.name, elapsed, len(tc.md))
		}
	}
}

func TestAuditW7_ImageResolver_SafeStorageExtRemoteSniffAndSVGDoctypeBlock(t *testing.T) {
	allowLocalhost(t)
	resolver := NewImageResolver()
	png := createTestPNG(200, 200)

	// 1. Referenced image with .html or backslash filename must never persist as .html/.js.
	fSvc := &mockFileService{}
	res := &types.ReadResult{
		MarkdownContent: `![](images/payload.html) ![](a.b\..\..\evil.js)`,
		ImageRefs: []types.ImageRef{
			{Filename: "payload.html", OriginalRef: "images/payload.html", MimeType: "text/html", ImageData: png},
			{Filename: `a.b\..\..\evil.js`, OriginalRef: `a.b\..\..\evil.js`, MimeType: "", ImageData: png},
		},
	}
	_, stored, err := resolver.ResolveAndStore(context.Background(), res, fSvc, 7)
	if err != nil {
		t.Fatalf("ResolveAndStore: %v", err)
	}
	if len(stored) != 2 || len(fSvc.saved) != 2 {
		t.Fatalf("expected 2 stored images, got stored=%d saved=%d", len(stored), len(fSvc.saved))
	}
	for _, entry := range fSvc.saved {
		if !strings.HasSuffix(entry.FileName, ".png") || strings.ContainsAny(entry.FileName, `/\`) {
			t.Fatalf("unsafe stored image filename %q (want clean .png)", entry.FileName)
		}
	}

	// 2. Inline SVG carrying <!DOCTYPE / <!ENTITY must be rejected.
	dtdSVG := `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY xxe "boom">]><svg xmlns="http://www.w3.org/2000/svg"><text>&xxe;</text></svg>`
	dtdMD := "![xxe](data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(dtdSVG)) + ")"
	dtdSvc := &mockFileService{}
	_, dtdImages, err := resolver.ResolveDataURIImages(context.Background(), dtdMD, dtdSvc, 7)
	if err != nil {
		t.Fatalf("ResolveDataURIImages: %v", err)
	}
	if len(dtdImages) != 0 || len(dtdSvc.saved) != 0 {
		t.Fatalf("expected SVG with <!DOCTYPE/<!ENTITY to be rejected, got %d saved", len(dtdSvc.saved))
	}

	// 3. Remote HTML spoofed as image/svg+xml and remote pixel-bomb PNG must be rejected.
	spoofHTML := "<!doctype html><html><body><script>alert(1)</script>" + strings.Repeat("A", 600) + "</body></html>"
	bombPNG := buildSpoofedDimensionsPNG(8000, 8000) // 64 MP > maxInlineImagePixels (16 MP)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spoof.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(spoofHTML))
		case "/bomb.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(bombPNG)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	remoteSvc := &mockFileService{}
	remoteMD := fmt.Sprintf("![s](%s/spoof.svg)\n\n![b](%s/bomb.png)", ts.URL, ts.URL)
	_, remoteImgs, err := resolver.ResolveRemoteImages(context.Background(), remoteMD, remoteSvc, 7)
	if err != nil {
		t.Fatalf("ResolveRemoteImages: %v", err)
	}
	if len(remoteImgs) != 0 || len(remoteSvc.saved) != 0 {
		t.Fatalf("expected spoofed SVG HTML and pixel-bomb PNG to be rejected, got %d saved", len(remoteSvc.saved))
	}
}

type overBudgetStreamServer struct {
	proto.UnimplementedDocReaderServer
}

func (s *overBudgetStreamServer) ReadStream(_ *proto.ReadRequest, stream grpc.ServerStreamingServer[proto.ReadStreamResponse]) error {
	if err := stream.Send(&proto.ReadStreamResponse{
		Payload: &proto.ReadStreamResponse_Meta{
			Meta: &proto.ReadStreamMeta{MarkdownContent: "![](a.png) ![](b.png) ![](c.png)"},
		},
	}); err != nil {
		return err
	}
	chunk := bytes.Repeat([]byte("x"), 2048)
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		if err := stream.Send(&proto.ReadStreamResponse{
			Payload: &proto.ReadStreamResponse_Image{
				Image: &proto.ImageRef{Filename: name, OriginalRef: name, MimeType: "image/png", ImageData: chunk},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func TestAuditW7_ImageBudgetAndGRPCCloseState(t *testing.T) {
	t.Setenv(mineruMaxImageBytesEnv, "4096")
	t.Setenv("DOCREADER_ALLOW_UNAUTHENTICATED", "true")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	proto.RegisterDocReaderServer(grpcSrv, &overBudgetStreamServer{})
	go func() { _ = grpcSrv.Serve(ln) }()
	t.Cleanup(grpcSrv.Stop)

	t.Setenv("DOCREADER_ADDR", ln.Addr().String())
	reader, err := NewGRPCDocumentReader(ln.Addr().String())
	if err != nil {
		t.Fatalf("NewGRPCDocumentReader: %v", err)
	}
	if !reader.IsConnected() {
		t.Fatal("expected IsConnected() == true before Close()")
	}

	_, err = reader.Read(context.Background(), &types.ReadRequest{FileName: "scan.pdf", FileType: "pdf", FileContent: []byte("%PDF")})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted when streamed images exceed budget, got %v", err)
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if reader.IsConnected() {
		t.Fatal("expected IsConnected() == false after Close()")
	}

	// Self-hosted PaddleOCRVLReader & MinerUReader must also enforce cumulative image byte cap.
	allowLocalhost(t)
	bigB64 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("p"), 3000))
	paddleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errorCode": 0,
			"result": map[string]any{
				"layoutParsingResults": []map[string]any{
					{
						"markdown": map[string]any{
							"text":   "![](img1.png)\n![](img2.png)",
							"images": map[string]string{"img1.png": bigB64, "img2.png": bigB64},
						},
					},
				},
			},
		})
	}))
	defer paddleSrv.Close()

	pReader := NewPaddleOCRVLReader(map[string]string{"paddleocr_vl_endpoint": paddleSrv.URL})
	if _, err := pReader.Read(context.Background(), &types.ReadRequest{FileContent: []byte("%PDF"), FileName: "doc.pdf", FileType: "pdf"}); err == nil {
		t.Fatal("expected PaddleOCRVLReader to reject cumulative images exceeding GENYEE_MINERU_MAX_IMAGE_BYTES")
	}
}

func TestAuditW7_CloudUploadFilenameSanitizationAndSourceBlocksFileTypeFallback(t *testing.T) {
	allowLocalhost(t)

	var uploadedPartFilename string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/jobs":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Errorf("multipart NextPart: %v", err)
					break
				}
				if p.FormName() == "file" {
					uploadedPartFilename = p.FileName()
				}
				_, _ = io.Copy(io.Discard, p)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"jobId": "job-1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/job-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"state":     "done",
					"resultUrl": map[string]any{"jsonUrl": srvURL + "/result.jsonl"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/result.jsonl":
			line := `{"result":{"layoutParsingResults":[{"prunedResult":{"page_size":[600,800],"parsing_res_list":[{"block_label":"text","block_content":"Hello PDF","block_bbox":[10,20,200,60]}]},"markdown":{"text":"Hello PDF","images":{}}}]}}`
			_, _ = w.Write([]byte(line + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	reader := NewPaddleOCRVLCloudReader(map[string]string{
		"paddleocr_vl_cloud_base_url": srv.URL + "/jobs",
		"paddleocr_vl_cloud_token":    "tok",
	})
	reader.pollInterval = 5 * time.Millisecond
	reader.timeout = 2 * time.Second
	// FileType is intentionally empty while FileName uses Windows backslashes + traversal.
	res, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF-1.4"),
		FileName:    `..\..\secret` + "\r\n" + `\report.pdf`,
		FileType:    "",
	})
	if err != nil {
		t.Fatalf("PaddleOCRVLCloudReader.Read: %v", err)
	}
	if uploadedPartFilename != "report.pdf" {
		t.Fatalf("uploaded multipart filename = %q, want sanitized %q", uploadedPartFilename, "report.pdf")
	}
	if len(res.SourceBlocks) == 0 {
		t.Fatal("expected non-empty SourceBlocks when FileType is empty and inferred from .pdf FileName")
	}
}

func TestAuditW7_ImageResolver_DistinctSameBasenameImagesNotCollided(t *testing.T) {
	resolver := NewImageResolver()
	fSvc := &mockFileService{}
	pngA := createTestPNG(200, 200)
	pngB := createTestPNG(240, 180) // distinct byte content

	res := &types.ReadResult{
		MarkdownContent: "![one](chap1/0.png)\n\n![two](chap2/0.png)",
		ImageRefs: []types.ImageRef{
			{Filename: "0.png", OriginalRef: "chap1/0.png", MimeType: "image/png", ImageData: pngA},
			{Filename: "0.png", OriginalRef: "chap2/0.png", MimeType: "image/png", ImageData: pngB},
		},
	}
	_, stored, err := resolver.ResolveAndStore(context.Background(), res, fSvc, 1)
	if err != nil {
		t.Fatalf("ResolveAndStore: %v", err)
	}
	if len(fSvc.saved) != 2 || len(stored) != 2 || stored[0].ServingURL == stored[1].ServingURL {
		t.Fatalf("distinct images sharing basename '0.png' collided: saved=%d stored=%+v", len(fSvc.saved), stored)
	}
}

func TestAuditW7_HTTPDocumentReader_MaxFileSizeTrustedAddrAndBareHostPort(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(httpReadResponse{MarkdownContent: "trusted ok"})
	}))
	defer srv.Close()

	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer otherSrv.Close()

	// 1. Operator-configured DOCREADER_ADDR (bare host:port) must work without SSRF_WHITELIST,
	// while non-configured loopback addresses remain blocked by SSRF validation.
	bareHostPort := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("DOCREADER_ADDR", bareHostPort)

	reader, err := NewHTTPDocumentReader(bareHostPort)
	if err != nil {
		t.Fatalf("NewHTTPDocumentReader(configured bare host:port) failed: %v", err)
	}
	got, err := reader.Read(context.Background(), &types.ReadRequest{FileName: "a.md", FileType: "md", FileContent: []byte("hi")})
	if err != nil || got.MarkdownContent != "trusted ok" {
		t.Fatalf("Read on operator-configured DOCREADER_ADDR = (%+v, %v), want 'trusted ok'", got, err)
	}

	if _, err := NewHTTPDocumentReader(otherSrv.URL); err == nil {
		t.Fatal("expected non-configured loopback HTTP docreader URL to be blocked by SSRF validation")
	}

	// 2. Oversized FileContent must fail fast with ResourceExhausted before marshalling/sending.
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	_, err = reader.Read(context.Background(), &types.ReadRequest{
		FileName:    "huge.pdf",
		FileType:    "pdf",
		FileContent: make([]byte, (1<<20)+1024),
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted for oversized FileContent, got %v", err)
	}
}

func TestAuditW7_CloudPollPathEscapeErrorPreviewAndHTMLTableFences(t *testing.T) {
	allowLocalhost(t)

	// 1. Poll URLs must percent-encode jobID / batchID path segments.
	var gotEscapedPath, gotRawQuery string
	pollSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		gotRawQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"state": "failed", "errorMsg": "stop"},
		})
	}))
	defer pollSrv.Close()

	cloudReader := NewPaddleOCRVLCloudReader(map[string]string{
		"paddleocr_vl_cloud_base_url": pollSrv.URL + "/jobs",
		"paddleocr_vl_cloud_token":    "tok",
	})
	cloudReader.pollInterval = 5 * time.Millisecond
	cloudReader.timeout = time.Second
	_, _ = cloudReader.pollJob(context.Background(), "job/../../admin?leak=1")
	if gotRawQuery != "" || !strings.Contains(gotEscapedPath, "%2F") || strings.Contains(gotEscapedPath, "/admin") {
		t.Fatalf("pollJob did not PathEscape jobID: path=%q query=%q", gotEscapedPath, gotRawQuery)
	}

	// 2. PaddleOCRVLReader non-200 error message must be capped by readErrorPreview, not full body.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("E", 64*1024)))
	}))
	defer errSrv.Close()

	selfPaddle := NewPaddleOCRVLReader(map[string]string{"paddleocr_vl_endpoint": errSrv.URL})
	_, err := selfPaddle.Read(context.Background(), &types.ReadRequest{FileContent: []byte("%PDF"), FileName: "x.pdf", FileType: "pdf"})
	if err == nil || len(err.Error()) > maxErrorPreviewBytes+256 {
		t.Fatalf("expected bounded error preview (<%d chars), got err len=%d", maxErrorPreviewBytes+256, len(fmt.Sprint(err)))
	}

	// 3. NormalizeHTMLTables must ignore mid-line ```code``` spans when tracking fenced code blocks.
	inputMD := "Use ```inline``` syntax:\n\n```html\n<table><tr><td>CodeExample</td></tr></table>\n```\n\n<table><tr><th>Header</th></tr><tr><td>RowVal</td></tr></table>"
	normalized := NormalizeHTMLTables(inputMD)
	if !strings.Contains(normalized, "```html\n<table><tr><td>CodeExample</td></tr></table>\n```") {
		t.Fatalf("fenced HTML table was corrupted after inline triple-backtick span:\n%s", normalized)
	}
	if !strings.Contains(normalized, "| Header |") || !strings.Contains(normalized, "| RowVal |") {
		t.Fatalf("prose HTML table after fenced block was not converted to GFM table:\n%s", normalized)
	}
}
