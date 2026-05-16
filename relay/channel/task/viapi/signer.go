package viapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SignRequest 计算 ACS3-HMAC-SHA256 签名 (阿里 OpenAPI V3 签名规范)。
// 返回完整 signed headers map (含 Authorization 与所有 x-acs-* 头)。
//
// 参数:
//   - method:   HTTP method (POST/GET)
//   - host:     如 "videoenhan.cn-shanghai.aliyuncs.com"
//   - path:     CanonicalURI，如 "/"
//   - query:    已编码的查询字符串（key=value&key=value，按 key 字典序）；空字符串可
//   - action:   x-acs-action 值，如 "EraseVideoSubtitles"
//   - version:  x-acs-version 值，如 "2020-03-20"
//   - baseHdrs: 调用方预填的额外 headers (host / content-type 必填；其他 x-acs-* 由本函数补全)
//   - body:     请求体原始字节
//   - ak / sk:  阿里 AccessKeyId / AccessKeySecret
//
// 调用方在自己生成的 nonce/date 不同步时可以预填 x-acs-date / x-acs-signature-nonce，
// signer 不会覆盖；若未填，本函数自动生成。
func SignRequest(method, host, path, query, action, version string, baseHdrs map[string]string, body []byte, ak, sk string) (map[string]string, error) {
	if method == "" || host == "" || action == "" || version == "" {
		return nil, fmt.Errorf("viapi signer: method/host/action/version are required")
	}
	if path == "" {
		path = "/"
	}

	hdrs := make(map[string]string, len(baseHdrs)+8)
	for k, v := range baseHdrs {
		// header name 统一小写
		hdrs[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	// 强制写入 host（即便调用方传了也以 host 参数为准）
	hdrs["host"] = host
	hdrs["x-acs-action"] = action
	hdrs["x-acs-version"] = version
	if _, ok := hdrs["x-acs-date"]; !ok {
		hdrs["x-acs-date"] = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	if _, ok := hdrs["x-acs-signature-nonce"]; !ok {
		nonce, err := generateNonce()
		if err != nil {
			return nil, fmt.Errorf("viapi signer: generate nonce failed: %w", err)
		}
		hdrs["x-acs-signature-nonce"] = nonce
	}
	// 请求体 SHA256（即便 body 为空也要计算 — 空字符串的 SHA256 是固定值）
	bodyHash := sha256Hex(body)
	hdrs["x-acs-content-sha256"] = bodyHash

	// 1. CanonicalRequest
	// 参与签名的 header 集合 = host + content-type (如果存在) + 所有 x-acs-* 头
	signableNames := make([]string, 0, len(hdrs))
	for name := range hdrs {
		if name == "host" || name == "content-type" || strings.HasPrefix(name, "x-acs-") {
			signableNames = append(signableNames, name)
		}
	}
	sort.Strings(signableNames)

	var canonHeadersBuf strings.Builder
	for _, name := range signableNames {
		canonHeadersBuf.WriteString(name)
		canonHeadersBuf.WriteString(":")
		canonHeadersBuf.WriteString(strings.TrimSpace(hdrs[name]))
		canonHeadersBuf.WriteString("\n")
	}
	signedHeaders := strings.Join(signableNames, ";")

	canonicalRequest := strings.Join([]string{
		strings.ToUpper(method),
		path,
		query,
		canonHeadersBuf.String(),
		signedHeaders,
		bodyHash,
	}, "\n")

	// 2. StringToSign
	stringToSign := "ACS3-HMAC-SHA256\n" + sha256Hex([]byte(canonicalRequest))

	// 3. Signature
	signature := hmacSha256Hex([]byte(sk), []byte(stringToSign))

	// 4. Authorization
	hdrs["authorization"] = fmt.Sprintf(
		"ACS3-HMAC-SHA256 Credential=%s,SignedHeaders=%s,Signature=%s",
		ak, signedHeaders, signature,
	)

	return hdrs, nil
}

// sha256Hex 计算 lowercase hex 编码的 SHA256
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// hmacSha256Hex 计算 lowercase hex 编码的 HMAC-SHA256
func hmacSha256Hex(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// generateNonce 生成随机 32-byte hex 字符串
func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RedactAuthorization 把 Authorization header 中的 AccessKey 部分打码，用于 debug 日志。
func RedactAuthorization(auth string) string {
	if auth == "" {
		return ""
	}
	// 格式: "ACS3-HMAC-SHA256 Credential=<AK>,SignedHeaders=...,Signature=..."
	idx := strings.Index(auth, "Credential=")
	if idx < 0 {
		return "***"
	}
	end := strings.Index(auth[idx:], ",")
	if end < 0 {
		return auth[:idx+len("Credential=")] + "***"
	}
	return auth[:idx+len("Credential=")] + "***" + auth[idx+end:]
}
