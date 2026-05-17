package volcvod

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SigningAlgorithm 火山引擎 V4 签名算法名（与 AWS SigV4 同源，区别仅在 service/region 字符串）
const SigningAlgorithm = "HMAC-SHA256"

// SignRequest 计算 Volcengine Signature V4 (HMAC-SHA256)。
//
// 参数:
//   - method:   HTTP method (POST/GET)
//   - host:     如 "vod.volcengineapi.com"
//   - path:     CanonicalURI（空时用 "/"）
//   - query:    url.Values，会按规范字典序 + 各字段 URL encode 拼成 canonical query
//   - baseHdrs: 调用方预填的额外 headers (host / content-type 会被自动补全 / 覆盖)
//   - body:     请求体原始字节（GET 类用 nil 即可）
//   - ak / sk:  AccessKeyId / AccessKeySecret
//   - region / service: cn-north-1 / vod
//
// 返回:
//   - 完整签名后的 headers map（含 Host / X-Date / X-Content-Sha256 / Authorization）
func SignRequest(method, host, path string, query url.Values, baseHdrs map[string]string, body []byte, ak, sk, region, service string) (map[string]string, error) {
	if method == "" || host == "" || ak == "" || sk == "" {
		return nil, fmt.Errorf("volcvod signer: method/host/ak/sk are required")
	}
	if path == "" {
		path = "/"
	}

	hdrs := make(map[string]string, len(baseHdrs)+4)
	for k, v := range baseHdrs {
		hdrs[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	// 强制 host 跟参数一致
	hdrs["host"] = host

	now := time.Now().UTC()
	if _, ok := hdrs["x-date"]; !ok {
		hdrs["x-date"] = now.Format("20060102T150405Z")
	}
	date := hdrs["x-date"]
	if len(date) < 8 {
		return nil, fmt.Errorf("volcvod signer: invalid x-date format")
	}
	shortDate := date[:8]

	bodyHash := sha256Hex(body)
	hdrs["x-content-sha256"] = bodyHash

	// 1. CanonicalRequest
	canonicalQuery := canonicalQueryString(query)
	signableNames := pickSignableHeaders(hdrs)
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
		canonicalQuery,
		canonHeadersBuf.String(),
		signedHeaders,
		bodyHash,
	}, "\n")

	// 2. StringToSign
	credentialScope := shortDate + "/" + region + "/" + service + "/request"
	stringToSign := strings.Join([]string{
		SigningAlgorithm,
		date,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// 3. Derive Signing Key
	signingKey := buildSigningKey(sk, shortDate, region, service)

	// 4. Signature
	signature := hex.EncodeToString(hmacSha256(signingKey, []byte(stringToSign)))

	// 5. Authorization header
	hdrs["authorization"] = fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		SigningAlgorithm, ak, credentialScope, signedHeaders, signature,
	)

	return hdrs, nil
}

// canonicalQueryString 火山规范的 query string：key 字典序，key 与 value 各自 URL encode，用 & 拼接
func canonicalQueryString(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(values))
	for _, k := range keys {
		vs := values[k]
		sort.Strings(vs) // multi-value 同名 key 也要 stable 排序
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// pickSignableHeaders 返回所有"参与签名"的 header 名（按字典序）
// 签名集合 = host + content-type (若存在) + 所有 x-* (含 x-date / x-content-sha256)
func pickSignableHeaders(hdrs map[string]string) []string {
	out := make([]string, 0, len(hdrs))
	for k := range hdrs {
		if k == "host" || k == "content-type" || strings.HasPrefix(k, "x-") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// buildSigningKey 火山 V4 派生 signing key (跟 AWS SigV4 同算法)
func buildSigningKey(sk, shortDate, region, service string) []byte {
	kDate := hmacSha256([]byte(sk), []byte(shortDate))
	kRegion := hmacSha256(kDate, []byte(region))
	kService := hmacSha256(kRegion, []byte(service))
	kSigning := hmacSha256(kService, []byte("request"))
	return kSigning
}

// sha256Hex 计算 lowercase hex 编码的 SHA256
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// hmacSha256 计算 HMAC-SHA256，返回 raw bytes
func hmacSha256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// RedactAuthorization 把 Authorization header 中的 AccessKey 部分打码，用于 debug 日志
func RedactAuthorization(auth string) string {
	if auth == "" {
		return ""
	}
	// 格式: "HMAC-SHA256 Credential=<AK>/...,SignedHeaders=...,Signature=..."
	idx := strings.Index(auth, "Credential=")
	if idx < 0 {
		return "***"
	}
	// 找到下一个 "/"，把 AK 替换为 ***
	rest := auth[idx+len("Credential="):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return auth[:idx+len("Credential=")] + "***"
	}
	return auth[:idx+len("Credential=")] + "***" + rest[slash:]
}
