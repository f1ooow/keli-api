package viapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// TaskAdaptor 阿里视觉智能 viapi 任务适配器
// 多个 viapi 子产品（videoenhan / imageseg ...）共用同一个 adaptor，
// 通过 model 名映射到 ActionTable 拿到具体的 Action / Product / Mode 信息。
type TaskAdaptor struct {
	taskcommon.BaseBilling

	ChannelType int
	accessKeyId string
	accessKeySecret string
	apiKey      string // 原 key，格式 "<ak>:<sk>"
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.apiKey = info.ApiKey
	// 解析 "<AccessKeyId>:<AccessKeySecret>"
	parts := strings.SplitN(info.ApiKey, ":", 2)
	if len(parts) == 2 {
		a.accessKeyId = strings.TrimSpace(parts[0])
		a.accessKeySecret = strings.TrimSpace(parts[1])
	}
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// 不强制要求 prompt（viapi 多数 Action 不需要 prompt）；只要求 model
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return wrapTaskError(err, "invalid_json", http.StatusBadRequest, true)
	}
	if strings.TrimSpace(req.Model) == "" {
		return wrapTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}
	cfg, ok := LookupAction(req.Model)
	if !ok {
		return wrapTaskError(fmt.Errorf("unsupported viapi model: %s", req.Model), "unsupported_model", http.StatusBadRequest, true)
	}
	// 单图模式兼容
	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{req.Image}
	}
	info.Action = cfg.Action
	// 复用 newapi 标准的 task_request 上下文存储
	c.Set("task_request", req)
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	cfg, ok := LookupActionByName(info.Action)
	if !ok {
		return "", fmt.Errorf("viapi: unknown action %q", info.Action)
	}
	route, ok := LookupRoute(cfg.Product)
	if !ok {
		return "", fmt.Errorf("viapi: unknown product %s", cfg.Product)
	}
	return fmt.Sprintf("https://%s/", route.Endpoint), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	cfg, ok := LookupActionByName(info.Action)
	if !ok {
		return fmt.Errorf("viapi: unknown action %q", info.Action)
	}
	route, ok := LookupRoute(cfg.Product)
	if !ok {
		return fmt.Errorf("viapi: unknown product %s", cfg.Product)
	}
	if a.accessKeyId == "" || a.accessKeySecret == "" {
		return fmt.Errorf("viapi: channel api_key must be '<AccessKeyId>:<AccessKeySecret>'")
	}

	// 读 body（已经被 DoApiRequest 设置在 req.Body 上）
	var bodyBytes []byte
	if req.Body != nil {
		buf, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			return errors.Wrap(readErr, "read body for signing")
		}
		bodyBytes = buf
		// 关键：reset body 以便后续真实发送
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}

	baseHdrs := map[string]string{
		"host":         route.Endpoint,
		"content-type": "application/x-www-form-urlencoded",
	}
	signed, err := SignRequest(req.Method, route.Endpoint, "/", "", cfg.Action, route.Version, baseHdrs, bodyBytes, a.accessKeyId, a.accessKeySecret)
	if err != nil {
		return errors.Wrap(err, "sign viapi request")
	}
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	// debug log（authorization redact）
	logger.LogDebug(c.Request.Context(), fmt.Sprintf("viapi sign: action=%s product=%s host=%s auth=%s", cfg.Action, cfg.Product, route.Endpoint, RedactAuthorization(signed["authorization"])))
	return nil
}

// BuildRequestBody 按 viapi RPC v3 约定，把请求参数打包成 application/x-www-form-urlencoded
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, getErr := relaycommon.GetTaskRequest(c)
	if getErr != nil {
		return nil, errors.Wrap(getErr, "get task request")
	}
	cfg, ok := LookupActionByName(info.Action)
	if !ok {
		return nil, fmt.Errorf("viapi: unknown action %q", info.Action)
	}
	params := url.Values{}
	// 把核心字段映射到 viapi 期望的参数名
	// 视频类：VideoURL；图像类：ImageURL；优先用 InputReference，回退 Images[0]
	srcURL := req.InputReference
	if srcURL == "" && len(req.Images) > 0 {
		srcURL = req.Images[0]
	}
	if srcURL != "" {
		switch cfg.Product {
		case "videoenhan", "videoseg":
			params.Set("VideoURL", srcURL)
		case "imageseg":
			params.Set("ImageURL", srcURL)
		default:
			// fallback: 同时填两个
			params.Set("VideoURL", srcURL)
			params.Set("ImageURL", srcURL)
		}
	}
	// metadata 里的字段透传（如 BX/BY/BW/BH 用于字幕擦除框；ReturnForm 用于分割）
	if req.Metadata != nil {
		keys := make([]string, 0, len(req.Metadata))
		for k := range req.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := req.Metadata[k]
			params.Set(k, fmt.Sprintf("%v", v))
		}
	}
	bodyStr := params.Encode()
	return strings.NewReader(bodyStr), nil
}

// DoRequest 构建并发送签名后的请求到 viapi 上游
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullURL, err := a.BuildRequestURL(info)
	if err != nil {
		return nil, errors.Wrap(err, "build request url failed")
	}
	httpReq, err := http.NewRequest(http.MethodPost, fullURL, requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "new http request failed")
	}
	// BuildRequestHeader 内部会读 body bytes 计算签名 + 重置 body
	if err := a.BuildRequestHeader(c, httpReq, info); err != nil {
		return nil, errors.Wrap(err, "build request header failed")
	}
	return service.GetHttpClient().Do(httpReq)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		taskErr = service.TaskErrorWrapper(readErr, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var parsed ViapiSubmitResponse
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", string(respBody)), "unmarshal_response_failed", http.StatusInternalServerError)
		return
	}
	if parsed.Code != "" && !strings.EqualFold(parsed.Code, "OK") {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("%s: %s", parsed.Code, parsed.Message), "viapi_api_error", resp.StatusCode)
		return
	}

	taskReq, _ := relaycommon.GetTaskRequest(c)
	cfg, _ := LookupActionByName(info.Action)

	if cfg.Mode == "sync" {
		// 同步：直接返回结果给 client，不走任务持久化
		openAIResp := dto.NewOpenAIVideo()
		openAIResp.ID = info.PublicTaskID
		openAIResp.TaskID = info.PublicTaskID
		openAIResp.Model = taskReq.Model
		openAIResp.Status = dto.VideoStatusCompleted
		openAIResp.CreatedAt = common.GetTimestamp()
		// 把结果 URL 装到 metadata.url
		resultURL := parsed.Data.ImageURL
		if resultURL == "" {
			resultURL = parsed.Data.VideoURL
		}
		openAIResp.SetMetadata("url", resultURL)
		openAIResp.SetMetadata("request_id", parsed.RequestId)
		if len(parsed.Data.Elements) > 0 {
			openAIResp.SetMetadata("elements", parsed.Data.Elements)
		}
		c.JSON(http.StatusOK, openAIResp)
		// taskID 返回 RequestId 仅用于日志，taskData 仅用于审计
		return parsed.RequestId, respBody, nil
	}

	// 异步：必须拿到 JobId
	if parsed.Data.JobId == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("job_id missing in response: %s", string(respBody)), "invalid_response", http.StatusInternalServerError)
		return
	}

	openAIResp := dto.NewOpenAIVideo()
	openAIResp.ID = info.PublicTaskID
	openAIResp.TaskID = info.PublicTaskID
	openAIResp.Model = taskReq.Model
	openAIResp.Status = dto.VideoStatusQueued
	openAIResp.CreatedAt = common.GetTimestamp()
	c.JSON(http.StatusOK, openAIResp)

	return parsed.Data.JobId, respBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	jobID, ok := body["task_id"].(string)
	if !ok || jobID == "" {
		return nil, fmt.Errorf("viapi: invalid task_id")
	}
	// 从 body 的 "action" 字段反查 product；newapi 在 polling 时会传入 task.Action
	action, _ := body["action"].(string)
	if action == "" {
		// 兜底：从 model 字段反查
		if modelName, ok := body["model"].(string); ok {
			if cfg, found := LookupAction(modelName); found {
				action = cfg.Action
			}
		}
	}
	cfg, cfgOk := LookupActionByName(action)
	if !cfgOk {
		return nil, fmt.Errorf("viapi: cannot resolve product for action %q (jobId=%s)", action, jobID)
	}
	route, routeOk := LookupRoute(cfg.Product)
	if !routeOk {
		return nil, fmt.Errorf("viapi: unknown product %s", cfg.Product)
	}

	// 拆分 key 为 ak:sk
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("viapi: invalid api_key format, expect '<ak>:<sk>'")
	}
	ak, sk := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	params := url.Values{}
	params.Set("JobId", jobID)
	bodyStr := params.Encode()
	bodyBytes := []byte(bodyStr)

	baseHdrs := map[string]string{
		"host":         route.Endpoint,
		"content-type": "application/x-www-form-urlencoded",
	}
	signed, err := SignRequest(http.MethodPost, route.Endpoint, "/", "", GetAsyncJobResultAction, route.Version, baseHdrs, bodyBytes, ak, sk)
	if err != nil {
		return nil, errors.Wrap(err, "sign GetAsyncJobResult")
	}

	httpReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://%s/", route.Endpoint), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, errors.Wrap(err, "new request for FetchTask")
	}
	for k, v := range signed {
		httpReq.Header.Set(k, v)
	}

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(httpReq)
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var parsed ViapiQueryResponse
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		return nil, errors.Wrap(err, "unmarshal viapi query response")
	}

	result := &relaycommon.TaskInfo{Code: 0}
	switch strings.ToUpper(parsed.Data.Status) {
	case "PROCESSING", "RUNNING", "QUEUED", "PENDING":
		result.Status = model.TaskStatusInProgress
	case "SUCCESS", "SUCCEEDED":
		result.Status = model.TaskStatusSuccess
		// Data.Result 是 JSON 字符串，解出 VideoURL / ImageURL
		var payload ViapiResultPayload
		if parsed.Data.Result != "" {
			if jsonErr := common.Unmarshal([]byte(parsed.Data.Result), &payload); jsonErr != nil {
				return nil, errors.Wrapf(jsonErr, "parse Result payload: %s", parsed.Data.Result)
			}
		}
		if payload.VideoURL != "" {
			result.Url = payload.VideoURL
		} else if payload.ImageURL != "" {
			result.Url = payload.ImageURL
		}
	case "FAIL", "FAILED", "CANCELED":
		result.Status = model.TaskStatusFailure
		if parsed.Data.ErrorMessage != "" {
			result.Reason = fmt.Sprintf("%s: %s", parsed.Data.ErrorCode, parsed.Data.ErrorMessage)
		} else if parsed.Message != "" {
			result.Reason = fmt.Sprintf("%s: %s", parsed.Code, parsed.Message)
		} else {
			result.Reason = "task failed"
		}
	default:
		result.Status = model.TaskStatusQueued
	}

	return result, nil
}

// EstimateBilling 按 per-call 或 per-minute 给出 OtherRatios。
// per-call: 返回空（按 ModelPrice × 1）；per-minute: 返回 seconds 倍率（无 duration 时默认 60 秒）
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	cfg, ok := LookupActionByName(info.Action)
	if !ok {
		return nil
	}
	if cfg.BillingMode == "per-minute" {
		seconds := 60
		if taskReq.Duration > 0 {
			seconds = taskReq.Duration
		}
		return map[string]float64{"seconds": float64(seconds)}
	}
	return nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList()
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

// wrapTaskError 包装错误为 dto.TaskError
func wrapTaskError(err error, code string, statusCode int, localError bool) *dto.TaskError {
	return &dto.TaskError{
		Code:       code,
		Message:    err.Error(),
		StatusCode: statusCode,
		LocalError: localError,
		Error:      err,
	}
}
