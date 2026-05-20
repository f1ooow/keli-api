package volccv

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
)

// 输入约束（火山 lens_lqir 官方限制）
const (
	MaxInputBytes  = 5 * 1024 * 1024 // 5 MB
	MaxInputWidth  = 2128
	MaxInputHeight = 4046
	MinInputDim    = 50
)

// preprocess 内部目标：缩到 2048 内 + 重编码到 5 MB 内
const (
	PreprocessMaxDim       = 2048
	PreprocessJpegQuality1 = 92
	PreprocessJpegQuality2 = 80
)

// PreprocessForLqir 把任意输入图片 bytes 处理成火山 lens_lqir 可接受的合规输入。
//
// 步骤：
//  1. 解码（自动识别 PNG/JPEG/BMP）
//  2. 若边长 > 2048 → 等比缩到 2048 内（draw.ApproxBiLinear，fit=inside）
//  3. 编码为 JPG q=92
//  4. 若仍 > 5MB → 重编码 q=80（再不行就报错，业务约定 CCS 端不应超 10MB）
//
// 返回：合规 bytes + 探测到的 width / height（编码后未必精确，但用编码前的 image.Bounds）。
func PreprocessForLqir(input []byte) (out []byte, width, height int, err error) {
	if len(input) == 0 {
		return nil, 0, 0, fmt.Errorf("preprocess: empty input")
	}

	// 1) 解码
	img, format, decodeErr := image.Decode(bytes.NewReader(input))
	if decodeErr != nil {
		return nil, 0, 0, fmt.Errorf("preprocess: decode %s failed: %w", format, decodeErr)
	}

	srcBounds := img.Bounds()
	srcW, srcH := srcBounds.Dx(), srcBounds.Dy()
	if srcW < MinInputDim || srcH < MinInputDim {
		return nil, srcW, srcH, fmt.Errorf("preprocess: image too small %dx%d (min %d)", srcW, srcH, MinInputDim)
	}

	// 2) 等比缩到 2048 内
	dstW, dstH := fitInside(srcW, srcH, PreprocessMaxDim)
	if dstW != srcW || dstH != srcH {
		dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, srcBounds, draw.Over, nil)
		img = dst
	}

	// 3) 编码 JPG q=92
	out, err = encodeJpeg(img, PreprocessJpegQuality1)
	if err != nil {
		return nil, dstW, dstH, fmt.Errorf("preprocess: jpeg encode q92 failed: %w", err)
	}

	// 4) 仍超 5MB → q=80 再压
	if len(out) > MaxInputBytes {
		out, err = encodeJpeg(img, PreprocessJpegQuality2)
		if err != nil {
			return nil, dstW, dstH, fmt.Errorf("preprocess: jpeg encode q80 failed: %w", err)
		}
	}

	if len(out) > MaxInputBytes {
		return nil, dstW, dstH, fmt.Errorf("preprocess: output still exceeds %d bytes after q=80 (%d)", MaxInputBytes, len(out))
	}

	return out, dstW, dstH, nil
}

// fitInside 等比缩放使长边 ≤ maxDim；短边按比例计算。两边都 ≤ maxDim 时不变。
func fitInside(w, h, maxDim int) (int, int) {
	if w <= maxDim && h <= maxDim {
		return w, h
	}
	if w >= h {
		newW := maxDim
		newH := int(float64(h) * float64(maxDim) / float64(w))
		if newH < 1 {
			newH = 1
		}
		return newW, newH
	}
	newH := maxDim
	newW := int(float64(w) * float64(maxDim) / float64(h))
	if newW < 1 {
		newW = 1
	}
	return newW, newH
}

func encodeJpeg(img image.Image, quality int) ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeImageBase64 helper: 把 base64 字符串或 data URL 解为 raw bytes
//
// 输入接受形式：
//   - 纯 base64（无前缀）
//   - data URL（"data:image/png;base64,XXXX..."）
func DecodeImageBase64(input string) ([]byte, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return nil, fmt.Errorf("empty base64 image input")
	}
	// 去除 data URL 前缀
	if idx := strings.Index(s, ","); idx >= 0 && strings.HasPrefix(s, "data:") {
		s = s[idx+1:]
	}
	return base64.StdEncoding.DecodeString(s)
}

// EncodeImageBase64 raw bytes → base64 字符串（无 data URL 前缀）
func EncodeImageBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// translateError 把火山 CV 错误码翻译成 newapi 友好的 (httpStatus, code, message)
//
// 翻译表（与 design.md 对齐）：
//
//	50411 / 50511 / 50412 / 50512 → 400 content_policy_violation
//	algorithm_base_resp 非 0     → 400 algorithm_error（透传 status_message）
//	5xx / 网络错误               → 503 service_unavailable（建议上层重试 1 次）
//	其他                          → 500 upstream_error
func translateError(resp *CVProcessResponse, httpStatus int) (int, string, string) {
	if resp == nil {
		return http.StatusBadGateway, "upstream_empty_response", "图像增强服务无响应"
	}

	switch resp.Code {
	case 50411:
		return http.StatusBadRequest, "content_policy_violation",
			"图片内容前置审核未通过，请更换图片"
	case 50511:
		return http.StatusBadRequest, "content_policy_violation",
			"增强结果后置审核未通过，请更换图片"
	case 50412, 50512:
		return http.StatusBadRequest, "content_policy_violation",
			"文本审核未通过"
	}

	// algorithm_base_resp 层错误
	if resp.Data != nil && resp.Data.AlgorithmBaseResp != nil && resp.Data.AlgorithmBaseResp.StatusCode != 0 {
		return http.StatusBadRequest, "algorithm_error",
			fmt.Sprintf("图像增强算法错误：%s", resp.Data.AlgorithmBaseResp.StatusMessage)
	}

	// httpStatus 5xx
	if httpStatus >= 500 && httpStatus < 600 {
		return http.StatusServiceUnavailable, "service_unavailable",
			"图像增强服务暂时不可用，请稍后重试"
	}

	if httpStatus >= 400 && httpStatus < 500 {
		msg := resp.Message
		if msg == "" {
			msg = "上游 4xx 错误"
		}
		return httpStatus, "upstream_error", fmt.Sprintf("图像增强失败：%s", msg)
	}

	// 兜底
	msg := resp.Message
	if msg == "" {
		msg = fmt.Sprintf("未知错误 (code=%d)", resp.Code)
	}
	return http.StatusInternalServerError, "upstream_error", fmt.Sprintf("图像增强失败：%s", msg)
}

// isSuccessResponse 判断响应是否成功（顶层 code=10000 且算法层 status_code=0）
func isSuccessResponse(resp *CVProcessResponse) bool {
	if resp == nil {
		return false
	}
	if resp.Code != 10000 {
		return false
	}
	if resp.Data == nil {
		return false
	}
	if resp.Data.AlgorithmBaseResp != nil && resp.Data.AlgorithmBaseResp.StatusCode != 0 {
		return false
	}
	return true
}

// extractResultURL 从成功响应里抽取唯一结果 URL（lens_lqir 单图模式）
func extractResultURL(resp *CVProcessResponse) string {
	if resp == nil || resp.Data == nil {
		return ""
	}
	if len(resp.Data.ImageUrls) > 0 {
		return resp.Data.ImageUrls[0]
	}
	return ""
}

// extractResultBase64 从成功响应里抽取唯一结果 base64（return_url=false 时使用）
func extractResultBase64(resp *CVProcessResponse) string {
	if resp == nil || resp.Data == nil {
		return ""
	}
	if len(resp.Data.BinaryDataBase64) > 0 {
		return resp.Data.BinaryDataBase64[0]
	}
	return ""
}

// 强制引用 png / bmp，确保 image.Decode 注册了 PNG / BMP / JPEG 解码器
// （JPEG 由 image/jpeg 注册；PNG / BMP 在导入时自动注册）
var (
	_ = png.Encode
	_ = bmp.Decode
)
