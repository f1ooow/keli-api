package volcvod

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// TaskAdaptor 火山引擎 VOD 媒体处理任务适配器
//
// 多个 VOD 媒体处理能力（Erase / AudioExtract / ...）共用同一个 adaptor，
// 通过 model 名映射到 ActionTable 决定 Operation.Task.Type 的具体值。
//
// 所有能力共用同一对 Action：StartExecution（提交）+ GetExecution（轮询）
type TaskAdaptor struct {
	taskcommon.BaseBilling

	ChannelType     int
	accessKeyId     string
	accessKeySecret string
	apiKey          string // 原 key，格式 "<ak>:<sk>"
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
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return wrapTaskError(err, "invalid_json", http.StatusBadRequest, true)
	}
	if strings.TrimSpace(req.Model) == "" {
		return wrapTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}
	cfg, ok := LookupAction(req.Model)
	if !ok {
		return wrapTaskError(fmt.Errorf("unsupported volcvod model: %s", req.Model), "unsupported_model", http.StatusBadRequest, true)
	}
	// VOD 输入必须是 Vid（点播空间内的视频 ID），由 CCS server 提前上传并提供
	if strings.TrimSpace(req.InputReference) == "" {
		return wrapTaskError(fmt.Errorf("input_reference (火山 Vid) is required"), "missing_input_reference", http.StatusBadRequest, true)
	}
	// 把 ActionTable 的 TaskType 暂存到 info.Action（复用 newapi 现有约定）
	info.Action = cfg.TaskType
	c.Set("task_request", req)
	c.Set("volcvod_action", cfg) // 缓存 cfg 给 BuildRequest* 用
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	// 提交任务的 URL（StartExecution）
	return fmt.Sprintf("https://%s/?Action=%s&Version=%s", Endpoint, ActionStartExecution, Version), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	if a.accessKeyId == "" || a.accessKeySecret == "" {
		return fmt.Errorf("volcvod: channel api_key must be '<AccessKeyId>:<AccessKeySecret>'")
	}

	// 读 body bytes（已由上游设置）
	var bodyBytes []byte
	if req.Body != nil {
		buf, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			return errors.Wrap(readErr, "read body for signing")
		}
		bodyBytes = buf
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}

	// Query 参数从 URL 上拆出来，参与签名
	query := req.URL.Query()

	baseHdrs := map[string]string{
		"host":         Endpoint,
		"content-type": "application/json",
	}
	signed, err := SignRequest(req.Method, Endpoint, req.URL.Path, query, baseHdrs, bodyBytes, a.accessKeyId, a.accessKeySecret, Region, Service)
	if err != nil {
		return errors.Wrap(err, "sign volcvod request")
	}
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	logger.LogDebug(c.Request.Context(), fmt.Sprintf("volcvod sign: action=%s host=%s auth=%s", info.Action, Endpoint, RedactAuthorization(signed["authorization"])))
	return nil
}

// BuildRequestBody 按火山 VOD StartExecution 约定，把请求参数打包成 application/json
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	taskReq, getErr := relaycommon.GetTaskRequest(c)
	if getErr != nil {
		return nil, errors.Wrap(getErr, "get task request")
	}
	cfgI, _ := c.Get("volcvod_action")
	cfg, ok := cfgI.(ActionConfig)
	if !ok {
		// 兜底从 model 名再查一次
		cfg, ok = LookupAction(taskReq.Model)
		if !ok {
			return nil, fmt.Errorf("volcvod: unable to resolve action for model %s", taskReq.Model)
		}
	}

	body := StartExecutionRequest{
		Input: InputSpec{
			Type: "Vid",
			Vid:  taskReq.InputReference,
		},
		Operation: OperationSpec{
			Type: "Task",
			Task: TaskSpec{Type: cfg.TaskType},
		},
	}

	switch cfg.TaskType {
	case "Erase":
		spec := &EraseSpec{
			Mode: "Auto",
			Auto: &EraseAutoSpec{
				Type:           "Subtitle",
				SubtitleFilter: map[string]interface{}{},
			},
			NewVid:        true,
			WithEraseInfo: false,
		}
		// metadata.locations 是 normalized 矩形数组（CCS server 透传过来）
		if taskReq.Metadata != nil {
			if locs, lok := taskReq.Metadata["locations"].([]interface{}); lok && len(locs) > 0 {
				spec.Auto.Locations = parseLocations(locs)
			}
		}
		body.Operation.Task.Erase = spec
	case "AudioExtract":
		body.Operation.Task.AudioExtract = &AudioExtractSpec{Voice: true}
	default:
		return nil, fmt.Errorf("volcvod: unsupported task type %s", cfg.TaskType)
	}

	bytesOut, marshalErr := common.Marshal(body)
	if marshalErr != nil {
		return nil, errors.Wrap(marshalErr, "marshal volcvod body")
	}
	return bytes.NewReader(bytesOut), nil
}

// parseLocations 把 metadata 里 [{"topLeftX":..., ...}] 转成 LocationSpec 数组
// 支持小写驼峰 (CCS server 习惯) 和大写驼峰 (火山 native)
func parseLocations(raw []interface{}) []LocationSpec {
	out := make([]LocationSpec, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		rect := RatioRect{}
		// 优先用 RatioLocation 嵌套（与火山 native 一致）
		if inner, found := m["RatioLocation"].(map[string]interface{}); found {
			rect = pickRatioRect(inner)
		} else if inner, found := m["ratioLocation"].(map[string]interface{}); found {
			rect = pickRatioRect(inner)
		} else {
			// 直接给四个字段
			rect = pickRatioRect(m)
		}
		out = append(out, LocationSpec{RatioLocation: rect})
	}
	return out
}

func pickRatioRect(m map[string]interface{}) RatioRect {
	pick := func(keys ...string) float64 {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if f, fok := toFloat(v); fok {
					return f
				}
			}
		}
		return 0
	}
	return RatioRect{
		TopLeftX:     pick("TopLeftX", "topLeftX"),
		TopLeftY:     pick("TopLeftY", "topLeftY"),
		BottomRightX: pick("BottomRightX", "bottomRightX"),
		BottomRightY: pick("BottomRightY", "bottomRightY"),
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// DoRequest 构建并发送签名后的请求到火山 VOD 上游
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullURL, err := a.BuildRequestURL(info)
	if err != nil {
		return nil, errors.Wrap(err, "build request url failed")
	}
	httpReq, err := http.NewRequest(http.MethodPost, fullURL, requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "new http request failed")
	}
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

	if resp.StatusCode >= 400 {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("volcvod %d: %s", resp.StatusCode, string(respBody)), "volcvod_api_error", resp.StatusCode)
		return
	}

	var parsed StartExecutionResponse
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", string(respBody)), "unmarshal_response_failed", http.StatusInternalServerError)
		return
	}
	if parsed.Result.RunId == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("run_id missing in response: %s", string(respBody)), "invalid_response", http.StatusInternalServerError)
		return
	}

	taskReq, _ := relaycommon.GetTaskRequest(c)
	openAIResp := dto.NewOpenAIVideo()
	openAIResp.ID = info.PublicTaskID
	openAIResp.TaskID = info.PublicTaskID
	openAIResp.Model = taskReq.Model
	openAIResp.Status = dto.VideoStatusQueued
	openAIResp.CreatedAt = common.GetTimestamp()
	c.JSON(http.StatusOK, openAIResp)

	return parsed.Result.RunId, respBody, nil
}

// FetchTask 调火山 GetExecution 查任务状态
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	runId, ok := body["task_id"].(string)
	if !ok || runId == "" {
		return nil, fmt.Errorf("volcvod: invalid task_id (run_id)")
	}

	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("volcvod: invalid api_key format, expect '<ak>:<sk>'")
	}
	ak, sk := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	query := url.Values{}
	query.Set("Action", ActionGetExecution)
	query.Set("Version", Version)
	query.Set("RunId", runId)

	fullURL := fmt.Sprintf("https://%s/?%s", Endpoint, query.Encode())

	baseHdrs := map[string]string{
		"host":         Endpoint,
		"content-type": "application/json",
	}
	signed, err := SignRequest(http.MethodGet, Endpoint, "/", query, baseHdrs, nil, ak, sk, Region, Service)
	if err != nil {
		return nil, errors.Wrap(err, "sign GetExecution")
	}

	httpReq, err := http.NewRequest(http.MethodGet, fullURL, nil)
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
	var parsed GetExecutionResponse
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		return nil, errors.Wrap(err, "unmarshal volcvod query response")
	}

	r := parsed.Result
	result := &relaycommon.TaskInfo{Code: 0}
	switch strings.ToLower(r.Status) {
	case "pending", "running", "queued":
		result.Status = model.TaskStatusInProgress
	case "success", "succeeded":
		result.Status = model.TaskStatusSuccess
		// TaskInfo 只有 Url / RemoteUrl / Reason / Progress 几个字段，不支持 Metadata。
		// 我们用约定的 vod-erase:// 或 vod-audio:// scheme 编码所有信息，
		// CCS server 端 volcvod driver 用 URL parser 解。
		// 格式：
		//   Erase:        vod-erase://<file_name>?vid=<vid>&duration=<s>&size=<bytes>
		//   AudioExtract: vod-audio://<voice_fn>?bg=<bg_fn>&duration=<s>&voice_size=<b>&bg_size=<b>
		result.Url = encodeVodResultUrl(r.Output.Task)
	case "failed", "fail", "canceled":
		result.Status = model.TaskStatusFailure
		if r.Error != nil {
			result.Reason = fmt.Sprintf("%s: %s", r.Error.Code, r.Error.Message)
		} else {
			result.Reason = "task failed"
		}
	default:
		result.Status = model.TaskStatusQueued
	}

	return result, nil
}

// encodeVodResultUrl 把 Output.Task 信息编码为 CCS server 能解析的 URL
//
// 之所以走 URL 编码而不是 metadata 字段，是因为 newapi 的 TaskInfo struct 只有
// Url / RemoteUrl / Reason / Progress 几个透传字段。本编码格式作为 newapi <-> CCS
// 之间的私有约定，CCS server 端 volcvod driver 应反向解析。
//
// 格式：
//
//	Erase:        vod-erase://<file_name>?vid=<vid>&duration=<seconds>&size=<bytes>
//	AudioExtract: vod-audio://<voice_fn>?bg=<bg_fn>&duration=<s>&voice_size=<b>&bg_size=<b>
func encodeVodResultUrl(task OutputTaskSpec) string {
	if task.Erase != nil {
		q := url.Values{}
		if task.Erase.File.Vid != "" {
			q.Set("vid", task.Erase.File.Vid)
		}
		if task.Erase.Duration > 0 {
			q.Set("duration", fmt.Sprintf("%v", task.Erase.Duration))
		}
		if task.Erase.File.Size != "" {
			q.Set("size", task.Erase.File.Size)
		}
		out := "vod-erase://" + task.Erase.File.FileName
		if encoded := q.Encode(); encoded != "" {
			out = out + "?" + encoded
		}
		return out
	}
	if task.AudioExtract != nil {
		q := url.Values{}
		if task.AudioExtract.Background.FileName != "" {
			q.Set("bg", task.AudioExtract.Background.FileName)
		}
		if task.AudioExtract.Duration > 0 {
			q.Set("duration", fmt.Sprintf("%v", task.AudioExtract.Duration))
		}
		if task.AudioExtract.Voice.Size != "" {
			q.Set("voice_size", task.AudioExtract.Voice.Size)
		}
		if task.AudioExtract.Background.Size != "" {
			q.Set("bg_size", task.AudioExtract.Background.Size)
		}
		out := "vod-audio://" + task.AudioExtract.Voice.FileName
		if encoded := q.Encode(); encoded != "" {
			out = out + "?" + encoded
		}
		return out
	}
	return ""
}

// EstimateBilling per-input-minute / per-output-minute 都需要 seconds 估算
// VOD 任务的 duration 来自上游响应（GetExecution 的 Output.Task.*.Duration）
// 这里 EstimateBilling 在提交时调用，还没有产物 duration，按 60s 兜底
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	cfg, ok := LookupAction(taskReq.Model)
	if !ok {
		return nil
	}
	if cfg.BillingMode == "per-input-minute" || cfg.BillingMode == "per-output-minute" {
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
