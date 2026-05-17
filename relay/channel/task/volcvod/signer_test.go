package volcvod

import (
	"net/url"
	"strings"
	"testing"
)

// TestSignRequest_StructuralIntegrity 验证签名后的 headers 包含必需字段且格式正确。
// 火山官方 Go example 暂未公开"完整签名 expected value"，所以这里做结构性测试 +
// 交叉验证（用 Python SDK 跑同样输入对比 Signature 是后续 TODO）。
//
// 必须验证的不变量：
//  1. 不报错
//  2. Authorization header 包含 HMAC-SHA256 Credential / SignedHeaders / Signature
//  3. SignedHeaders 至少包含 host, content-type, x-date, x-content-sha256
//  4. Signature 是 64 个 lowercase hex 字符（SHA256 输出长度）
//  5. x-content-sha256 是 SHA256(body) 的 lowercase hex
//  6. x-date 格式是 20060102T150405Z
func TestSignRequest_StructuralIntegrity(t *testing.T) {
	const (
		method = "POST"
		host   = "vod.volcengineapi.com"
		path   = "/"
		ak     = "AKLT0000000000000000000000000000"
		sk     = "U0VDUkVUMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMA=="
	)

	query := url.Values{}
	query.Set("Action", "StartExecution")
	query.Set("Version", "2025-01-01")

	body := []byte(`{"Input":{"Type":"Vid","Vid":"v0d25cg100"},"Operation":{"Type":"Task","Task":{"Type":"Erase"}}}`)

	baseHdrs := map[string]string{
		"host":         host,
		"content-type": "application/json",
		"x-date":       "20260518T160900Z", // 固定时间，便于交叉验证
	}

	hdrs, err := SignRequest(method, host, path, query, baseHdrs, body, ak, sk, Region, Service)
	if err != nil {
		t.Fatalf("SignRequest failed: %v", err)
	}

	// (1) Authorization 必须存在
	auth, ok := hdrs["authorization"]
	if !ok {
		t.Fatal("missing authorization header")
	}

	// (2) 算法名
	if !strings.HasPrefix(auth, SigningAlgorithm+" ") {
		t.Errorf("authorization should start with %q, got: %s", SigningAlgorithm, auth)
	}

	// (3) 包含 Credential / SignedHeaders / Signature
	for _, key := range []string{"Credential=" + ak + "/", "SignedHeaders=", "Signature="} {
		if !strings.Contains(auth, key) {
			t.Errorf("authorization missing %q, got: %s", key, auth)
		}
	}

	// (4) SignedHeaders 必须包含基本头
	idx := strings.Index(auth, "SignedHeaders=")
	if idx < 0 {
		t.Fatal("SignedHeaders missing")
	}
	end := strings.Index(auth[idx:], ", ")
	if end < 0 {
		t.Fatal("SignedHeaders end delimiter missing")
	}
	signedHeaders := auth[idx+len("SignedHeaders=") : idx+end]
	for _, must := range []string{"host", "content-type", "x-content-sha256", "x-date"} {
		if !strings.Contains(signedHeaders, must) {
			t.Errorf("SignedHeaders missing %q: %s", must, signedHeaders)
		}
	}

	// (5) Signature 长度 = 64
	sigIdx := strings.Index(auth, "Signature=")
	if sigIdx < 0 {
		t.Fatal("Signature= missing")
	}
	sig := auth[sigIdx+len("Signature="):]
	if len(sig) != 64 {
		t.Errorf("Signature should be 64 hex chars, got %d: %s", len(sig), sig)
	}
	for _, c := range sig {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("Signature contains non-hex char %q: %s", c, sig)
			break
		}
	}

	// (6) x-content-sha256 = SHA256(body) lowercase hex (64 chars)
	contentHash, ok := hdrs["x-content-sha256"]
	if !ok {
		t.Fatal("x-content-sha256 missing")
	}
	if len(contentHash) != 64 {
		t.Errorf("x-content-sha256 should be 64 hex chars, got %d: %s", len(contentHash), contentHash)
	}
	// SHA256 of the exact body bytes — 用 sha256Hex 内部函数验证
	expectedHash := sha256Hex(body)
	if contentHash != expectedHash {
		t.Errorf("x-content-sha256 mismatch:\n  got:      %s\n  expected: %s", contentHash, expectedHash)
	}
}

// TestSignRequest_EmptyBody 验证 GET 请求（无 body）能正确签名。
// 火山 GetExecution 是 GET，body 应为 nil，sha256 hex = e3b0c442...
func TestSignRequest_EmptyBody(t *testing.T) {
	const (
		method = "GET"
		host   = "vod.volcengineapi.com"
		path   = "/"
		ak     = "AKLT0000000000000000000000000000"
		sk     = "U0VDUkVUMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMA=="
	)

	query := url.Values{}
	query.Set("Action", "GetExecution")
	query.Set("Version", "2025-01-01")
	query.Set("RunId", "hb:fakerunid001")

	baseHdrs := map[string]string{
		"host":         host,
		"content-type": "application/json",
		"x-date":       "20260518T160900Z",
	}

	hdrs, err := SignRequest(method, host, path, query, baseHdrs, nil, ak, sk, Region, Service)
	if err != nil {
		t.Fatalf("SignRequest failed: %v", err)
	}

	const emptyBodySha = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if hdrs["x-content-sha256"] != emptyBodySha {
		t.Errorf("empty body sha256 mismatch: got %s, expected %s", hdrs["x-content-sha256"], emptyBodySha)
	}
}

// TestSignRequest_Deterministic 验证同样输入两次产生同样输出（同 x-date）
// 这是 e2e 重试与单测稳定性的基础。
func TestSignRequest_Deterministic(t *testing.T) {
	query := url.Values{}
	query.Set("Action", "GetExecution")
	query.Set("Version", "2025-01-01")
	query.Set("RunId", "test")

	baseHdrs := map[string]string{
		"host":         "vod.volcengineapi.com",
		"content-type": "application/json",
		"x-date":       "20260518T160900Z",
	}

	hdrs1, err1 := SignRequest("GET", "vod.volcengineapi.com", "/", query, baseHdrs, nil, "ak", "sk", Region, Service)
	hdrs2, err2 := SignRequest("GET", "vod.volcengineapi.com", "/", query, baseHdrs, nil, "ak", "sk", Region, Service)
	if err1 != nil || err2 != nil {
		t.Fatalf("SignRequest errors: %v / %v", err1, err2)
	}
	if hdrs1["authorization"] != hdrs2["authorization"] {
		t.Errorf("non-deterministic signature:\n  1: %s\n  2: %s", hdrs1["authorization"], hdrs2["authorization"])
	}
}

// TestSignRequest_CanonicalQueryString 验证 query string 顺序无关性（火山要求字典序）
func TestSignRequest_CanonicalQueryString(t *testing.T) {
	q1 := url.Values{}
	q1.Set("Action", "GetExecution")
	q1.Set("Version", "2025-01-01")
	q1.Set("RunId", "test")

	q2 := url.Values{}
	q2.Set("Version", "2025-01-01")
	q2.Set("RunId", "test")
	q2.Set("Action", "GetExecution")

	baseHdrs := map[string]string{
		"host":         "vod.volcengineapi.com",
		"content-type": "application/json",
		"x-date":       "20260518T160900Z",
	}

	h1, _ := SignRequest("GET", "vod.volcengineapi.com", "/", q1, baseHdrs, nil, "ak", "sk", Region, Service)
	h2, _ := SignRequest("GET", "vod.volcengineapi.com", "/", q2, baseHdrs, nil, "ak", "sk", Region, Service)
	if h1["authorization"] != h2["authorization"] {
		t.Errorf("query order should not affect signature:\n  q1: %s\n  q2: %s", h1["authorization"], h2["authorization"])
	}
}

// TestRedactAuthorization 验证 AccessKey 不会泄漏到 debug 日志
func TestRedactAuthorization(t *testing.T) {
	auth := "HMAC-SHA256 Credential=AKLT5tABC1234DEF/20260518/cn-north-1/vod/request, SignedHeaders=host;x-content-sha256;x-date, Signature=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	redacted := RedactAuthorization(auth)
	if strings.Contains(redacted, "AKLT5tABC1234DEF") {
		t.Errorf("AccessKey leaked in redacted output: %s", redacted)
	}
	if !strings.Contains(redacted, "abcdef0123") {
		t.Errorf("Signature unexpectedly redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "***") {
		t.Errorf("redact marker missing: %s", redacted)
	}
}
