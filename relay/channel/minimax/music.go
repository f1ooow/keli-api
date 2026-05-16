package minimax

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// MiniMaxMusicResponse /v1/music_generation 响应（仅用于日志，不参与计费）
type MiniMaxMusicResponse struct {
	Data      MiniMaxMusicData  `json:"data,omitempty"`
	ExtraInfo MiniMaxMusicExtra `json:"extra_info,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
	BaseResp  MiniMaxBaseResp   `json:"base_resp"`
}

type MiniMaxMusicData struct {
	Audio  string `json:"audio,omitempty"`
	Status int    `json:"status,omitempty"`
}

type MiniMaxMusicExtra struct {
	MusicDuration int `json:"music_duration,omitempty"`
}

// RelayMiniMaxMusic 透明转发 /v1/music_generation 到 MiniMax。
// 计费：按次扣 ModelPrice × 1（由 controller 层处理）。music_duration 仅作日志。
func RelayMiniMaxMusic(c *gin.Context, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	storage, storageErr := common.GetBodyStorage(c)
	if storageErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read music_generation request body: %w", storageErr),
			types.ErrorCodeReadRequestBodyFailed,
			http.StatusBadRequest,
		)
	}
	rawBody, rbErr := storage.Bytes()
	if rbErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read storage bytes: %w", rbErr),
			types.ErrorCodeReadRequestBodyFailed,
			http.StatusBadRequest,
		)
	}

	baseURL := info.ChannelBaseUrl
	if baseURL == "" {
		baseURL = "https://api.minimax.chat"
	}
	upstreamURL := fmt.Sprintf("%s/v1/music_generation", baseURL)

	req, reqErr := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(rawBody))
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

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read upstream response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}

	// 透传响应
	for k, vs := range resp.Header {
		if !service.ShouldCopyUpstreamHeader(c, k, vs) {
			continue
		}
		for _, v := range vs {
			c.Header(k, v)
		}
	}
	c.Data(resp.StatusCode, "application/json", respBody)

	return &dto.Usage{TotalTokens: 0}, nil
}
