package minimax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// MiniMaxVoiceCloneRequest /v1/voice_clone 请求体
// 注意：file_id 跨 HTTP 边界用 string 表示（避免 JS Number 精度问题），
// json:",string" tag 自动从 string 反序列化为 int64 发给 MiniMax。
type MiniMaxVoiceCloneRequest struct {
	FileID  int64  `json:"file_id,string"`
	VoiceID string `json:"voice_id"`
	// 以下字段如果带，会触发 MiniMax 端的试听文本合成，按字符计费
	Text             string `json:"text,omitempty"`
	Model            string `json:"model,omitempty"`
	NeedNoiseReduce  *bool  `json:"need_noise_reduce,omitempty"`
	NeedVolumeNormal *bool  `json:"need_volume_normalization,omitempty"`
	Accuracy         *int   `json:"accuracy,omitempty"`
	Aigc_Watermark   *bool  `json:"aigc_watermark,omitempty"`
}

// MiniMaxVoiceCloneResponse /v1/voice_clone 响应体
type MiniMaxVoiceCloneResponse struct {
	InputSensitive     bool             `json:"input_sensitive,omitempty"`
	InputSensitiveType int              `json:"input_sensitive_type,omitempty"`
	DemoAudio          string           `json:"demo_audio,omitempty"`
	ExtraInfo          MiniMaxExtraInfo `json:"extra_info,omitempty"`
	BaseResp           MiniMaxBaseResp  `json:"base_resp"`
}

// RelayMiniMaxVoiceClone 透明转发 /v1/voice_clone。
// 关键设计：voice_id 由 client 自定义，MiniMax 不回写；newapi 不修改响应；
// CCS server 端负责从自己刚发的请求体里读 voice_id 持久化。
func RelayMiniMaxVoiceClone(c *gin.Context, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	// 反序列化请求体（可重复读，因为 storage 已缓存）
	// file_id 的 string→int64 通过 struct tag `json:"file_id,string"` 自动处理
	var parsed MiniMaxVoiceCloneRequest
	if unmarshalErr := common.UnmarshalBodyReusable(c, &parsed); unmarshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal voice_clone request: %w", unmarshalErr),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}
	if parsed.VoiceID == "" {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("voice_id is required"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}
	if parsed.FileID == 0 {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("file_id is required"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}

	// 重新序列化为 MiniMax 期望的 JSON（file_id 写成 int64 数字）
	upstreamBody, marshalErr := json.Marshal(struct {
		FileID  int64  `json:"file_id"`
		VoiceID string `json:"voice_id"`
		// 复用其他字段
		Text             string `json:"text,omitempty"`
		Model            string `json:"model,omitempty"`
		NeedNoiseReduce  *bool  `json:"need_noise_reduce,omitempty"`
		NeedVolumeNormal *bool  `json:"need_volume_normalization,omitempty"`
		Accuracy         *int   `json:"accuracy,omitempty"`
		Aigc_Watermark   *bool  `json:"aigc_watermark,omitempty"`
	}{
		FileID:           parsed.FileID,
		VoiceID:          parsed.VoiceID,
		Text:             parsed.Text,
		Model:            parsed.Model,
		NeedNoiseReduce:  parsed.NeedNoiseReduce,
		NeedVolumeNormal: parsed.NeedVolumeNormal,
		Accuracy:         parsed.Accuracy,
		Aigc_Watermark:   parsed.Aigc_Watermark,
	})
	if marshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to marshal upstream request: %w", marshalErr),
			types.ErrorCodeInvalidRequest,
			http.StatusInternalServerError,
		)
	}

	baseURL := info.ChannelBaseUrl
	if baseURL == "" {
		baseURL = "https://api.minimax.chat"
	}
	upstreamURL := fmt.Sprintf("%s/v1/voice_clone", baseURL)

	req, reqErr := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if reqErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to build upstream request: %w", reqErr),
			types.ErrorCodeBadResponse,
			http.StatusInternalServerError,
		)
	}
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, doErr := service.GetHttpClient().Do(req)
	if doErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to call minimax upstream: %w", doErr),
			types.ErrorCodeBadResponse,
			http.StatusBadGateway,
		)
	}
	defer resp.Body.Close()

	respBody, readBodyErr := io.ReadAll(resp.Body)
	if readBodyErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read upstream response: %w", readBodyErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}

	// 透传上游响应（不修改 schema）
	for k, vs := range resp.Header {
		if !service.ShouldCopyUpstreamHeader(c, k, vs) {
			continue
		}
		for _, v := range vs {
			c.Header(k, v)
		}
	}
	c.Data(resp.StatusCode, "application/json", respBody)

	// 解析响应以统计 usage_characters（带 text+model 时按字符计费）
	totalTokens := 0
	if parsed.Text != "" && parsed.Model != "" {
		var parsedResp MiniMaxVoiceCloneResponse
		if jsonErr := json.Unmarshal(respBody, &parsedResp); jsonErr == nil {
			totalTokens = int(parsedResp.ExtraInfo.UsageCharacters)
		} else {
			logger.LogWarn(c, fmt.Sprintf("voice_clone response unmarshal failed: %s", jsonErr.Error()))
		}
	}

	return &dto.Usage{
		PromptTokens:     0,
		CompletionTokens: 0,
		TotalTokens:      totalTokens,
	}, nil
}
