package volccv

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relay/channel/task/volcvod"
)

// ---------------------------------------------------------------------------
// SigV4 签名结构性测试
//
// 实质是验证 "volccv 用 volcvod.SignRequest 时 service=cv 没问题"。详细签名
// 算法测试见 volcvod/signer_test.go。
// ---------------------------------------------------------------------------

func TestSignRequest_VolccvServiceParam(t *testing.T) {
	const (
		method = "POST"
		host   = Endpoint // visual.volcengineapi.com
		path   = "/"
		ak     = "AKLT0000000000000000000000000000"
		sk     = "U0VDUkVUMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMA=="
	)

	query := url.Values{}
	query.Set("Action", ActionCVProcess)
	query.Set("Version", Version)

	body := []byte(`{"req_key":"lens_lqir","binary_data_base64":["AAAA"]}`)

	baseHdrs := map[string]string{
		"host":         host,
		"content-type": "application/json",
		"x-date":       "20260520T120000Z",
	}

	hdrs, err := volcvod.SignRequest(method, host, path, query, baseHdrs, body, ak, sk, Region, Service)
	if err != nil {
		t.Fatalf("SignRequest failed: %v", err)
	}

	auth, ok := hdrs["authorization"]
	if !ok {
		t.Fatal("missing authorization header")
	}

	// 1) 算法名前缀
	if !strings.HasPrefix(auth, "HMAC-SHA256 ") {
		t.Errorf("authorization should start with %q, got: %s", "HMAC-SHA256", auth)
	}

	// 2) credential scope 必须包含 region/service = cn-north-1/cv
	wantScope := "/" + Region + "/" + Service + "/request"
	if !strings.Contains(auth, wantScope) {
		t.Errorf("credential scope missing %q in: %s", wantScope, auth)
	}

	// 3) Signature 64 hex
	idx := strings.Index(auth, "Signature=")
	if idx < 0 {
		t.Fatal("Signature= missing")
	}
	sig := auth[idx+len("Signature="):]
	if len(sig) != 64 {
		t.Errorf("Signature should be 64 hex chars, got %d", len(sig))
	}

	// 4) x-content-sha256 必填
	if _, ok := hdrs["x-content-sha256"]; !ok {
		t.Fatal("x-content-sha256 missing")
	}

	// 5) x-date 透传
	if hdrs["x-date"] != "20260520T120000Z" {
		t.Errorf("x-date should be passed through: got %q", hdrs["x-date"])
	}
}

func TestSignRequest_ServiceMatters(t *testing.T) {
	// 同一 host/key/body 用 service=cv vs service=vod 必须产生不同签名
	const (
		method = "POST"
		host   = Endpoint
		path   = "/"
		ak     = "AKLTtest"
		sk     = "secrettest"
	)

	query := url.Values{}
	query.Set("Action", ActionCVProcess)
	query.Set("Version", Version)
	body := []byte(`{"req_key":"lens_lqir"}`)

	baseHdrs := map[string]string{
		"host":         host,
		"content-type": "application/json",
		"x-date":       "20260520T120000Z",
	}

	hdrsCv, _ := volcvod.SignRequest(method, host, path, query, baseHdrs, body, ak, sk, Region, "cv")
	hdrsVod, _ := volcvod.SignRequest(method, host, path, query, baseHdrs, body, ak, sk, Region, "vod")

	if hdrsCv["authorization"] == hdrsVod["authorization"] {
		t.Errorf("service parameter must affect signature: both signatures equal: %s", hdrsCv["authorization"])
	}
}

// ---------------------------------------------------------------------------
// Preprocess 测试
// ---------------------------------------------------------------------------

func makeFakePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// 随机像素让 png 不那么容易压缩到很小（便于触发体积上限测试）
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 13) % 256),
				G: uint8((y * 17) % 256),
				B: uint8(((x + y) * 7) % 256),
				A: 255,
			})
		}
	}
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestPreprocess_ResizesOversizedImage(t *testing.T) {
	// 输入 3000x4000 PNG → 应被等比缩到 ≤2048 内
	input := makeFakePNG(3000, 4000)
	out, w, h, err := PreprocessForLqir(input)
	if err != nil {
		t.Fatalf("PreprocessForLqir failed: %v", err)
	}
	if w > PreprocessMaxDim || h > PreprocessMaxDim {
		t.Errorf("preprocess did not resize to ≤ %d: got %dx%d", PreprocessMaxDim, w, h)
	}
	// 比例必须保持（3:4）：原 3000:4000 → 缩后 1536:2048
	if w != 1536 || h != 2048 {
		t.Errorf("expected resize to 1536x2048 (ratio preserve), got %dx%d", w, h)
	}
	// 应为合法 JPG，能 decode
	if _, _, decodeErr := image.Decode(bytes.NewReader(out)); decodeErr != nil {
		t.Errorf("preprocess output not decodable: %v", decodeErr)
	}
	if len(out) > MaxInputBytes {
		t.Errorf("preprocess output exceeds 5MB: %d bytes", len(out))
	}
}

func TestPreprocess_KeepsSmallImage(t *testing.T) {
	// 800x600 PNG → 缩放维度不变（≤2048）
	input := makeFakePNG(800, 600)
	out, w, h, err := PreprocessForLqir(input)
	if err != nil {
		t.Fatalf("PreprocessForLqir failed: %v", err)
	}
	if w != 800 || h != 600 {
		t.Errorf("small image should not be resized: got %dx%d", w, h)
	}
	if len(out) == 0 {
		t.Errorf("preprocess returned empty bytes")
	}
}

func TestPreprocess_OutputUnder5MB(t *testing.T) {
	// 边界尺寸 2128x4046 (火山上限) → 缩到 2048 内 → JPG 编码后必须 ≤5MB
	input := makeFakePNG(2128, 4046)
	out, w, h, err := PreprocessForLqir(input)
	if err != nil {
		t.Fatalf("PreprocessForLqir failed: %v", err)
	}
	if len(out) > MaxInputBytes {
		t.Errorf("output > 5MB after preprocess: %d bytes (w=%d h=%d)", len(out), w, h)
	}
	if w > PreprocessMaxDim || h > PreprocessMaxDim {
		t.Errorf("output dim > %d after preprocess: %dx%d", PreprocessMaxDim, w, h)
	}
}

func TestPreprocess_TooSmallRejected(t *testing.T) {
	// 30x30 < MinInputDim=50 → 应报错
	input := makeFakePNG(30, 30)
	_, _, _, err := PreprocessForLqir(input)
	if err == nil {
		t.Errorf("expected error for too-small image (30x30), got nil")
	}
}

func TestPreprocess_EmptyInputRejected(t *testing.T) {
	_, _, _, err := PreprocessForLqir(nil)
	if err == nil {
		t.Errorf("expected error for empty input, got nil")
	}
}

func TestFitInside(t *testing.T) {
	cases := []struct{ w, h, max, wantW, wantH int }{
		{800, 600, 2048, 800, 600},        // 不需要缩
		{3000, 4000, 2048, 1536, 2048},     // 等比缩，h 为长边
		{4000, 3000, 2048, 2048, 1536},     // 等比缩，w 为长边
		{2048, 2048, 2048, 2048, 2048},     // 刚好等于
		{4096, 4096, 2048, 2048, 2048},     // 1:1 缩半
	}
	for _, tc := range cases {
		gotW, gotH := fitInside(tc.w, tc.h, tc.max)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("fitInside(%d,%d,%d) = %dx%d, want %dx%d",
				tc.w, tc.h, tc.max, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}

// ---------------------------------------------------------------------------
// Base64 helper 测试
// ---------------------------------------------------------------------------

func TestDecodeImageBase64_StripsDataURLPrefix(t *testing.T) {
	// 准备一个 hello bytes 的合法 base64
	const raw = "aGVsbG8=" // "hello"

	cases := []string{
		raw,
		"data:image/png;base64," + raw,
		"data:image/jpeg;base64," + raw,
		"   " + raw + "   ", // 周围空白
	}
	for _, in := range cases {
		out, err := DecodeImageBase64(in)
		if err != nil {
			t.Errorf("DecodeImageBase64(%q) failed: %v", in, err)
			continue
		}
		if string(out) != "hello" {
			t.Errorf("DecodeImageBase64(%q) = %q, want %q", in, string(out), "hello")
		}
	}
}

func TestDecodeImageBase64_EmptyRejected(t *testing.T) {
	if _, err := DecodeImageBase64(""); err == nil {
		t.Errorf("empty input should error")
	}
	if _, err := DecodeImageBase64("    "); err == nil {
		t.Errorf("whitespace-only input should error")
	}
}

// ---------------------------------------------------------------------------
// 错误码翻译测试
// ---------------------------------------------------------------------------

func TestTranslateError_ContentPolicyViolations(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		wantStat int
		wantCode string
	}{
		{"50411 input policy", 50411, http.StatusBadRequest, "content_policy_violation"},
		{"50511 output policy", 50511, http.StatusBadRequest, "content_policy_violation"},
		{"50412 text input policy", 50412, http.StatusBadRequest, "content_policy_violation"},
		{"50512 text output policy", 50512, http.StatusBadRequest, "content_policy_violation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &CVProcessResponse{Code: tc.code, Status: tc.code}
			status, code, msg := translateError(resp, http.StatusOK)
			if status != tc.wantStat {
				t.Errorf("status: got %d, want %d", status, tc.wantStat)
			}
			if code != tc.wantCode {
				t.Errorf("code: got %q, want %q", code, tc.wantCode)
			}
			if msg == "" {
				t.Errorf("message should not be empty")
			}
		})
	}
}

func TestTranslateError_5xxServiceUnavailable(t *testing.T) {
	resp := &CVProcessResponse{Code: 0, Message: "internal error"}
	status, code, _ := translateError(resp, http.StatusInternalServerError)
	if status != http.StatusServiceUnavailable {
		t.Errorf("5xx upstream should map to 503, got %d", status)
	}
	if code != "service_unavailable" {
		t.Errorf("code: got %q, want service_unavailable", code)
	}
}

func TestTranslateError_AlgorithmBaseRespError(t *testing.T) {
	resp := &CVProcessResponse{
		Code: 10000,
		Data: &CVProcessData{
			AlgorithmBaseResp: &AlgorithmBaseResp{
				StatusCode:    500,
				StatusMessage: "model timeout",
			},
		},
	}
	status, code, msg := translateError(resp, http.StatusOK)
	if status != http.StatusBadRequest {
		t.Errorf("algorithm_base_resp error should map to 400, got %d", status)
	}
	if code != "algorithm_error" {
		t.Errorf("code: got %q, want algorithm_error", code)
	}
	if !strings.Contains(msg, "model timeout") {
		t.Errorf("message should include upstream status_message: %s", msg)
	}
}

func TestTranslateError_NilResponse(t *testing.T) {
	status, code, _ := translateError(nil, 0)
	if status != http.StatusBadGateway {
		t.Errorf("nil response should map to 502, got %d", status)
	}
	if code != "upstream_empty_response" {
		t.Errorf("code: got %q, want upstream_empty_response", code)
	}
}

// ---------------------------------------------------------------------------
// 成功响应判定 + 结果抽取测试
// ---------------------------------------------------------------------------

func TestIsSuccessResponse(t *testing.T) {
	// 标准成功
	ok := &CVProcessResponse{
		Code: 10000,
		Data: &CVProcessData{
			AlgorithmBaseResp: &AlgorithmBaseResp{StatusCode: 0, StatusMessage: "success"},
			ImageUrls:         []string{"https://result/a.png"},
		},
	}
	if !isSuccessResponse(ok) {
		t.Errorf("standard success should pass isSuccessResponse")
	}

	// 顶层 code 不对
	bad1 := &CVProcessResponse{Code: 50411}
	if isSuccessResponse(bad1) {
		t.Errorf("non-10000 code should not be success")
	}

	// data 为 nil
	bad2 := &CVProcessResponse{Code: 10000}
	if isSuccessResponse(bad2) {
		t.Errorf("nil data should not be success")
	}

	// algorithm 层错
	bad3 := &CVProcessResponse{
		Code: 10000,
		Data: &CVProcessData{
			AlgorithmBaseResp: &AlgorithmBaseResp{StatusCode: 500},
		},
	}
	if isSuccessResponse(bad3) {
		t.Errorf("algorithm_base_resp non-zero should not be success")
	}
}

func TestExtractResultURL(t *testing.T) {
	resp := &CVProcessResponse{
		Data: &CVProcessData{
			ImageUrls: []string{"https://result/a.png", "https://result/b.png"},
		},
	}
	if got := extractResultURL(resp); got != "https://result/a.png" {
		t.Errorf("extractResultURL: got %q, want first url", got)
	}

	empty := &CVProcessResponse{Data: &CVProcessData{}}
	if got := extractResultURL(empty); got != "" {
		t.Errorf("empty urls should return empty string, got %q", got)
	}

	if got := extractResultURL(nil); got != "" {
		t.Errorf("nil resp should return empty string, got %q", got)
	}
}

func TestExtractResultBase64(t *testing.T) {
	resp := &CVProcessResponse{
		Data: &CVProcessData{
			BinaryDataBase64: []string{"AAAA", "BBBB"},
		},
	}
	if got := extractResultBase64(resp); got != "AAAA" {
		t.Errorf("extractResultBase64: got %q, want first base64", got)
	}
}

// ---------------------------------------------------------------------------
// OpenAI-images-edit 响应包装格式测试
// ---------------------------------------------------------------------------

func TestOpenAIImagesEditResponse_Shape(t *testing.T) {
	r := OpenAIImagesEditResponse{
		Created: 1716192000,
		Data: []OpenAIImagesEditDataItem{
			{URL: "https://result/a.png", Width: 2048, Height: 1536},
		},
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	s := string(out)

	// CCS server 端解析依赖以下字段名：
	for _, key := range []string{
		`"created"`, `"data"`, `"url"`, `"width"`, `"height"`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("response missing %q in: %s", key, s)
		}
	}

	// width=0 时不应出现 width 字段（omitempty）
	r2 := OpenAIImagesEditResponse{
		Created: 0,
		Data:    []OpenAIImagesEditDataItem{{URL: "https://x"}},
	}
	out2, _ := json.Marshal(r2)
	if strings.Contains(string(out2), `"width"`) {
		t.Errorf("width=0 should be omitted: %s", string(out2))
	}
}

// ---------------------------------------------------------------------------
// Lookup / Registry 测试
// ---------------------------------------------------------------------------

func TestLookupAction(t *testing.T) {
	cfg, ok := LookupAction("volc-lens-lqir")
	if !ok {
		t.Fatalf("volc-lens-lqir should be registered")
	}
	if cfg.ReqKey != "lens_lqir" {
		t.Errorf("ReqKey: got %q, want lens_lqir", cfg.ReqKey)
	}
	if cfg.Mode != "sync" {
		t.Errorf("Mode: got %q, want sync", cfg.Mode)
	}
	if cfg.BillingMode != "per-call" {
		t.Errorf("BillingMode: got %q, want per-call", cfg.BillingMode)
	}

	if _, ok := LookupAction("does-not-exist"); ok {
		t.Errorf("unknown model should not be found")
	}
}

func TestLookupActionByReqKey(t *testing.T) {
	cfg, ok := LookupActionByReqKey("lens_lqir")
	if !ok {
		t.Fatalf("lens_lqir req_key should be registered")
	}
	if cfg.Pricing != 0.01 {
		t.Errorf("Pricing: got %v, want 0.01", cfg.Pricing)
	}
}

func TestModelList(t *testing.T) {
	list := ModelList()
	if len(list) == 0 {
		t.Fatalf("ModelList is empty")
	}
	found := false
	for _, m := range list {
		if m == "volc-lens-lqir" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ModelList missing volc-lens-lqir: %v", list)
	}
}
