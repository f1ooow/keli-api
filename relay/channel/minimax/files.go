package minimax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// MiniMaxFileObject 上传后单个文件的元信息（file_id 跨边界使用 string 避免 JS Number 精度问题）
type MiniMaxFileObject struct {
	FileID    int64  `json:"file_id,string"`
	Bytes     int64  `json:"bytes,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Purpose   string `json:"purpose,omitempty"`
}

// MiniMaxFileResponse 上传响应
type MiniMaxFileResponse struct {
	File     MiniMaxFileObject `json:"file"`
	BaseResp MiniMaxBaseResp   `json:"base_resp"`
}

// RelayMiniMaxFilesUpload 把 multipart 请求重打包后转发到 MiniMax /v1/files/upload，
// 并对响应里的 file_id (int64) 序列化为 string 后回写给 client。
// 计费：quota = 0（上传本身不收费）。
func RelayMiniMaxFilesUpload(c *gin.Context, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	formData, parseErr := common.ParseMultipartFormReusable(c)
	if parseErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to parse multipart form: %w", parseErr),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}

	// 重新打包 multipart
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// 透传所有文本字段
	for key, values := range formData.Value {
		for _, value := range values {
			if writeErr := writer.WriteField(key, value); writeErr != nil {
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("failed to write form field %s: %w", key, writeErr),
					types.ErrorCodeInvalidRequest,
					http.StatusBadRequest,
				)
			}
		}
	}

	// 透传所有文件字段
	for fieldName, fileHeaders := range formData.File {
		for _, fh := range fileHeaders {
			file, openErr := fh.Open()
			if openErr != nil {
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("failed to open uploaded file: %w", openErr),
					types.ErrorCodeInvalidRequest,
					http.StatusBadRequest,
				)
			}
			part, partErr := writer.CreateFormFile(fieldName, fh.Filename)
			if partErr != nil {
				file.Close()
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("failed to create form file: %w", partErr),
					types.ErrorCodeInvalidRequest,
					http.StatusBadRequest,
				)
			}
			if _, copyErr := io.Copy(part, file); copyErr != nil {
				file.Close()
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("failed to copy file content: %w", copyErr),
					types.ErrorCodeInvalidRequest,
					http.StatusBadRequest,
				)
			}
			file.Close()
		}
	}
	if closeErr := writer.Close(); closeErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to close multipart writer: %w", closeErr),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}

	baseURL := info.ChannelBaseUrl
	if baseURL == "" {
		baseURL = "https://api.minimax.chat"
	}
	upstreamURL := fmt.Sprintf("%s/v1/files/upload", baseURL)

	req, reqErr := http.NewRequest(http.MethodPost, upstreamURL, &requestBody)
	if reqErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to build upstream request: %w", reqErr),
			types.ErrorCodeBadResponse,
			http.StatusInternalServerError,
		)
	}
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

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

	// 反序列化 + 序列化以确保 file_id 转为 string
	var parsed MiniMaxFileResponse
	if unmarshalErr := json.Unmarshal(respBody, &parsed); unmarshalErr != nil {
		// 无法解析，原样回传（保留状态码）
		logger.LogWarn(c, fmt.Sprintf("minimax files upload: unmarshal failed, passthrough: %s", unmarshalErr.Error()))
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

	if parsed.BaseResp.StatusCode != 0 {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("minimax files upload error: %d - %s", parsed.BaseResp.StatusCode, parsed.BaseResp.StatusMsg),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
		)
	}

	// 用 json.Marshal 重新序列化（FileID 因 struct tag 自动 string 化）
	out, marshalErr := json.Marshal(parsed)
	if marshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to marshal response: %w", marshalErr),
			types.ErrorCodeBadResponse,
			http.StatusInternalServerError,
		)
	}

	c.Data(http.StatusOK, "application/json", out)

	// 不扣费但要走完链路
	return &dto.Usage{TotalTokens: 0}, nil
}
