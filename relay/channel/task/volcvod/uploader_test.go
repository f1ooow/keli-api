package volcvod

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubHTTPClient is a deterministic HTTP fake for VOD upload tests.
//
// Each call advances `i` through `responses`; if the request matches an
// expected URL fragment we also record it in `seen`.
type stubHTTPClient struct {
	responses []stubResponse
	i         int
	seen      []stubRequest
}

type stubResponse struct {
	status int
	body   string
	// optional headers to set on the response
	headers map[string]string
}

type stubRequest struct {
	method string
	url    string
	body   []byte
	auth   string
}

func (s *stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body := []byte{}
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = b
	}
	s.seen = append(s.seen, stubRequest{
		method: req.Method,
		url:    req.URL.String(),
		body:   body,
		auth:   req.Header.Get("Authorization"),
	})
	if s.i >= len(s.responses) {
		return nil, fmt.Errorf("stubHTTPClient: ran out of responses at index %d", s.i)
	}
	resp := s.responses[s.i]
	s.i++
	r := &http.Response{
		StatusCode: resp.status,
		Body:       io.NopCloser(bytes.NewReader([]byte(resp.body))),
		Header:     http.Header{},
	}
	for k, v := range resp.headers {
		r.Header.Set(k, v)
	}
	return r, nil
}

func TestUploadMediaToVOD_EmptyBody(t *testing.T) {
	_, err := UploadMediaToVOD([]byte{}, VodUploadOpts{
		SpaceName: "test", AccessKey: "ak", SecretKey: "sk",
	}, "audio/mpeg")
	if err == nil {
		t.Fatal("expected empty_file error, got nil")
	}
	var vErr *VodUploadError
	if !errors.As(err, &vErr) || vErr.Code != "empty_file" {
		t.Fatalf("expected VodUploadError empty_file, got %v", err)
	}
}

func TestUploadMediaToVOD_TooLarge(t *testing.T) {
	big := make([]byte, VodSinglePutLimitBytes+1)
	_, err := UploadMediaToVOD(big, VodUploadOpts{
		SpaceName: "test", AccessKey: "ak", SecretKey: "sk",
	}, "video/mp4")
	if err == nil {
		t.Fatal("expected file_too_large error")
	}
	var vErr *VodUploadError
	if !errors.As(err, &vErr) || vErr.Code != "file_too_large_for_single_put" {
		t.Fatalf("expected VodUploadError file_too_large_for_single_put, got %v", err)
	}
}

func TestUploadMediaToVOD_MissingCredentials(t *testing.T) {
	_, err := UploadMediaToVOD([]byte("xx"), VodUploadOpts{SpaceName: "s"}, "audio/mpeg")
	if err == nil {
		t.Fatal("expected missing_credentials error")
	}
	var vErr *VodUploadError
	if !errors.As(err, &vErr) || vErr.Code != "missing_credentials" {
		t.Fatalf("expected missing_credentials, got %v", err)
	}
}

func TestUploadMediaToVOD_MissingSpace(t *testing.T) {
	_, err := UploadMediaToVOD([]byte("xx"), VodUploadOpts{AccessKey: "ak", SecretKey: "sk"}, "audio/mpeg")
	if err == nil {
		t.Fatal("expected missing_space error")
	}
	var vErr *VodUploadError
	if !errors.As(err, &vErr) || vErr.Code != "missing_space" {
		t.Fatalf("expected missing_space, got %v", err)
	}
}

func TestUploadMediaToVOD_HappyPath(t *testing.T) {
	stub := &stubHTTPClient{
		responses: []stubResponse{
			// 1. ApplyUploadInfo
			{
				status: 200,
				body: `{
					"ResponseMetadata": {},
					"Result": {
						"Data": {
							"UploadAddress": {
								"SessionKey": "sess-123",
								"StoreInfos": [{"StoreUri": "video/abc/foo.mp3", "Auth": "tos-auth"}],
								"UploadHosts": ["tos-cn-shanghai.volces.com"]
							}
						}
					}
				}`,
			},
			// 2. TOS PUT
			{status: 200, body: `{"success": 0}`},
			// 3. CommitUploadInfo
			{
				status: 200,
				body: `{
					"ResponseMetadata": {},
					"Result": {
						"Data": {
							"Vid": "v-abc-123",
							"SourceInfo": {
								"FileName": "video/abc/foo.mp3",
								"Duration": 12.34,
								"Size": 4096
							}
						}
					}
				}`,
			},
		},
	}
	prev := SetVodHTTPClientForTests(stub)
	defer SetVodHTTPClientForTests(prev)

	body := []byte("hello world")
	res, err := UploadMediaToVOD(body, VodUploadOpts{
		SpaceName: "kc-space", Region: "cn-north-1",
		AccessKey: "AKLT-xxx", SecretKey: "secret-xxx",
		FileExtension: ".mp3",
		FileName:      "narration.mp3",
	}, "audio/mpeg")
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
	if res.Vid != "v-abc-123" {
		t.Errorf("Vid = %q, want v-abc-123", res.Vid)
	}
	if res.FileName != "video/abc/foo.mp3" {
		t.Errorf("FileName = %q", res.FileName)
	}
	if res.Duration != 12.34 {
		t.Errorf("Duration = %v", res.Duration)
	}

	// Verify the wire sequence
	if len(stub.seen) != 3 {
		t.Fatalf("expected 3 HTTP calls, got %d", len(stub.seen))
	}
	if !strings.Contains(stub.seen[0].url, "Action=ApplyUploadInfo") {
		t.Errorf("call[0] url should have Action=ApplyUploadInfo: %s", stub.seen[0].url)
	}
	if !strings.Contains(stub.seen[0].url, "SpaceName=kc-space") {
		t.Errorf("call[0] url should have SpaceName=kc-space: %s", stub.seen[0].url)
	}
	// TOS PUT
	if stub.seen[1].method != "PUT" {
		t.Errorf("call[1] method = %s, want PUT", stub.seen[1].method)
	}
	if stub.seen[1].auth != "tos-auth" {
		t.Errorf("call[1] auth = %q, want tos-auth (verbatim from ApplyUploadInfo)", stub.seen[1].auth)
	}
	if !bytes.Equal(stub.seen[1].body, body) {
		t.Errorf("call[1] body mismatch")
	}
	// Commit
	if !strings.Contains(stub.seen[2].url, "Action=CommitUploadInfo") {
		t.Errorf("call[2] url should have Action=CommitUploadInfo: %s", stub.seen[2].url)
	}
	if !strings.Contains(stub.seen[2].url, "GetMeta") {
		t.Errorf("call[2] url should have GetMeta in Functions param: %s", stub.seen[2].url)
	}
}

func TestUploadMediaToVOD_ApplyError(t *testing.T) {
	stub := &stubHTTPClient{
		responses: []stubResponse{
			{
				status: 200,
				body: `{
					"ResponseMetadata": {"Error": {"Code": "InvalidParameter", "Message": "bad space"}},
					"Result": {}
				}`,
			},
		},
	}
	prev := SetVodHTTPClientForTests(stub)
	defer SetVodHTTPClientForTests(prev)

	_, err := UploadMediaToVOD([]byte("xx"), VodUploadOpts{
		SpaceName: "bad", AccessKey: "ak", SecretKey: "sk",
	}, "audio/mpeg")
	var vErr *VodUploadError
	if !errors.As(err, &vErr) || vErr.Code != "apply_upload_InvalidParameter" {
		t.Fatalf("expected apply_upload_InvalidParameter, got %v", err)
	}
}

func TestUploadMediaToVOD_TosPutBadSuccess(t *testing.T) {
	stub := &stubHTTPClient{
		responses: []stubResponse{
			{
				status: 200,
				body: `{
					"ResponseMetadata": {},
					"Result": {"Data": {"UploadAddress": {
						"SessionKey": "s", "StoreInfos": [{"StoreUri": "x", "Auth": "a"}], "UploadHosts": ["h"]
					}}}
				}`,
			},
			{status: 200, body: `{"success": 1, "payload": {"err": "x"}}`},
		},
	}
	prev := SetVodHTTPClientForTests(stub)
	defer SetVodHTTPClientForTests(prev)

	_, err := UploadMediaToVOD([]byte("xx"), VodUploadOpts{
		SpaceName: "s", AccessKey: "ak", SecretKey: "sk",
	}, "audio/mpeg")
	var vErr *VodUploadError
	if !errors.As(err, &vErr) || vErr.Code != "tos_put_bad_success" {
		t.Fatalf("expected tos_put_bad_success, got %v", err)
	}
}

func TestInferFromFilename(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		wantExt     string
		wantCtype   string
		wantFormat  string
	}{
		{"mp3", "x.mp3", ".mp3", "audio/mpeg", "mp3"},
		{"wav", "narration.wav", ".wav", "audio/wav", "wav"},
		{"mp4 upper", "X.MP4", ".mp4", "video/mp4", "mp4"},
		{"unknown", "file.xyz", ".xyz", "application/octet-stream", "mp3"},
		{"no ext", "file", "", "application/octet-stream", "mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferExtFromFilename(tt.filename); got != tt.wantExt {
				t.Errorf("InferExtFromFilename(%q) = %q, want %q", tt.filename, got, tt.wantExt)
			}
			if got := InferContentTypeFromFilename(tt.filename); got != tt.wantCtype {
				t.Errorf("InferContentTypeFromFilename(%q) = %q, want %q", tt.filename, got, tt.wantCtype)
			}
			if got := InferAudioFormatFromFilename(tt.filename); got != tt.wantFormat {
				t.Errorf("InferAudioFormatFromFilename(%q) = %q, want %q", tt.filename, got, tt.wantFormat)
			}
		})
	}
}

// Ensure the request body field in JSON is what we expect, since the signer
// signs it and a drift would invalidate signatures.
func TestApplyUploadInfo_ParamsAreSigned(t *testing.T) {
	stub := &stubHTTPClient{
		responses: []stubResponse{
			{status: 200, body: `{"ResponseMetadata": {}, "Result": {"Data": {}}}`},
		},
	}
	prev := SetVodHTTPClientForTests(stub)
	defer SetVodHTTPClientForTests(prev)

	_, _ = UploadMediaToVOD([]byte("xx"), VodUploadOpts{
		SpaceName: "s", AccessKey: "ak", SecretKey: "sk",
	}, "audio/mpeg")
	if len(stub.seen) < 1 {
		t.Fatal("no calls captured")
	}
	urlStr := stub.seen[0].url
	if !strings.Contains(urlStr, "FileType=video") {
		t.Errorf("expected FileType=video in URL: %s", urlStr)
	}
	if !strings.Contains(urlStr, "FileSize=2") { // len("xx") = 2
		t.Errorf("expected FileSize=2 in URL: %s", urlStr)
	}
}

// Smoke test: vod_uploader response shapes have the JSON tags we expect.
func TestApplyUploadResponse_JSONShape(t *testing.T) {
	in := []byte(`{"Result":{"Data":{"UploadAddress":{"SessionKey":"k","StoreInfos":[{"StoreUri":"u","Auth":"a"}],"UploadHosts":["h"]}}}}`)
	var parsed applyUploadResponse
	if err := json.Unmarshal(in, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Result.Data.UploadAddress.SessionKey != "k" {
		t.Errorf("SessionKey not parsed")
	}
	if len(parsed.Result.Data.UploadAddress.StoreInfos) != 1 {
		t.Errorf("StoreInfos not parsed")
	}
}
