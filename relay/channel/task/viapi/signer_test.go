package viapi

import (
	"strings"
	"testing"
)

// TestSignRequest_GoldenVector 使用阿里官方文档的金本位示例做回归。
// 来源：https://help.aliyun.com/zh/sdk/product-overview/v3-request-structure-and-signature
//
// 输入:
//   - method=POST
//   - host=ecs.cn-shanghai.aliyuncs.com
//   - action=RunInstances, version=2014-05-26
//   - 查询：ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd & RegionId=cn-shanghai
//   - body 为空（SHA256 = e3b0c442...）
//   - x-acs-date=2023-10-26T10:22:32Z
//   - x-acs-signature-nonce=3156853299f313e23d1673dc12e1703d
//   - AccessKeyId=YourAccessKeyId, AccessKeySecret=YourAccessKeySecret
//
// 期望 Signature = 06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0
func TestSignRequest_GoldenVector(t *testing.T) {
	const (
		method      = "POST"
		host        = "ecs.cn-shanghai.aliyuncs.com"
		path        = "/"
		query       = "ImageId=win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd&RegionId=cn-shanghai"
		action      = "RunInstances"
		version     = "2014-05-26"
		ak          = "YourAccessKeyId"
		sk          = "YourAccessKeySecret"
		expectedSig = "06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0"
	)

	baseHdrs := map[string]string{
		"host":                  host,
		"x-acs-date":            "2023-10-26T10:22:32Z",
		"x-acs-signature-nonce": "3156853299f313e23d1673dc12e1703d",
	}

	hdrs, err := SignRequest(method, host, path, query, action, version, baseHdrs, nil, ak, sk)
	if err != nil {
		t.Fatalf("SignRequest failed: %v", err)
	}

	auth, ok := hdrs["authorization"]
	if !ok {
		t.Fatal("missing authorization header in result")
	}

	// 提取 Signature 部分
	sigIdx := strings.Index(auth, "Signature=")
	if sigIdx < 0 {
		t.Fatalf("Signature= not found in authorization: %s", auth)
	}
	gotSig := auth[sigIdx+len("Signature="):]
	if gotSig != expectedSig {
		t.Errorf("Signature mismatch:\n  got:      %s\n  expected: %s\n  full auth: %s", gotSig, expectedSig, auth)
	}

	// 同时验证 content-sha256（空 body 的 SHA256 是已知常量）
	const emptyBodySha = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if hdrs["x-acs-content-sha256"] != emptyBodySha {
		t.Errorf("x-acs-content-sha256 mismatch: got %s, expected %s", hdrs["x-acs-content-sha256"], emptyBodySha)
	}
}

// TestRedactAuthorization 验证 AccessKey 不会泄漏到 debug 日志
func TestRedactAuthorization(t *testing.T) {
	auth := "ACS3-HMAC-SHA256 Credential=LTAI5tABC1234DEF,SignedHeaders=host;x-acs-action,Signature=abcdef"
	redacted := RedactAuthorization(auth)
	if strings.Contains(redacted, "LTAI5tABC1234DEF") {
		t.Errorf("AccessKey leaked in redacted output: %s", redacted)
	}
	if !strings.Contains(redacted, "Signature=abcdef") {
		t.Errorf("Signature unexpectedly redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "***") {
		t.Errorf("redact marker missing: %s", redacted)
	}
}
