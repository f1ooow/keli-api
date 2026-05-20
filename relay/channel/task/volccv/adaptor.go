package volccv

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/relay/channel/task/volcvod"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// TaskAdaptor 火山引擎 CV (Computer Vision) 图像处理任务适配器
//
// 多个 CV 能力（lens_lqir / lens_nnsr2_pic_common / ...）共用同一个 adaptor，
// 通过 model 名映射到 ActionTable 决定 body.req_key 的具体值。
//
// 与 volcvod 不同：
//   - volcvod 是异步（StartExecution/GetExecution 配对），CV 是同步（CVProcess 一次返回）
//   - volcvod 走 task 表轮询，volccv 直接 c.JSON 返回结果（参考 viapi sync 模式）
//   - host / service：vod.volcengineapi.com / vod  vs  visual.volcengineapi.com / cv
//
// SigV4 签名直接复用 volcvod.SignRequest，区别仅在 service 参数。
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
		return wrapTaskError(fmt.Errorf("unsupported volccv model: %s", req.Model), "unsupported_model", http.StatusBadRequest, true)
	}

	// 兼容 Image（单图字符串） / Images（数组）。lens_lqir 单图模式，取第一张即可。
	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{req.Image}
	}
	if len(req.Images) == 0 {
		return wrapTaskError(
			fmt.Errorf("at least one image (base64 or data URL) is required"),
			"missing_image", http.StatusBadRequest, true,
		)
	}

	info.Action = cfg.ReqKey
	c.Set("task_request", req)
	c.Set("volccv_action", cfg)
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	// CVProcess 接口：visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31
	return fmt.Sprintf("https://%s/?Action=%s&Version=%s", Endpoint, ActionCVProcess, Version), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	if a.accessKeyId == "" || a.accessKeySecret == "" {
		return fmt.Errorf("volccv: channel api_key must be '<AccessKeyId>:<AccessKeySecret>'")
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
	// 关键：复用 volcvod.SignRequest，仅改 region/service 参数
	signed, err := volcvod.SignRequest(
		req.Method, Endpoint, req.URL.Path, query, baseHdrs, bodyBytes,
		a.accessKeyId, a.accessKeySecret, Region, Service,
	)
	if err != nil {
		return errors.Wrap(err, "sign volccv request")
	}
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	logger.LogDebug(c.Request.Context(), fmt.Sprintf(
		"volccv sign: action=%s host=%s auth=%s",
		info.Action, Endpoint, volcvod.RedactAuthorization(signed["authorization"]),
	))
	return nil
}

// BuildRequestBody 按火山 CVProcess 约定，把 TaskSubmitReq 打包成 application/json
//
// 一键无参收敛（设计决议）：boundary=2k / hdr=false / wb=false / format=PNG / return_url=true。
// preprocess 在这里发生：保证输入图合规 (≤5MB, ≤2128/4046, JPG/PNG/BMP)。
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	taskReq, getErr := relaycommon.GetTaskRequest(c)
	if getErr != nil {
		return nil, errors.Wrap(getErr, "get task request")
	}
	cfgI, _ := c.Get("volccv_action")
	cfg, ok := cfgI.(ActionConfig)
	if !ok {
		cfg, ok = LookupAction(taskReq.Model)
		if !ok {
			return nil, fmt.Errorf("volccv: unable to resolve action for model %s", taskReq.Model)
		}
	}

	if len(taskReq.Images) == 0 {
		return nil, fmt.Errorf("volccv: no input image found in request")
	}

	// 1) decode base64 / data URL
	rawIn, decodeErr := DecodeImageBase64(taskReq.Images[0])
	if decodeErr != nil {
		return nil, errors.Wrap(decodeErr, "volccv: decode input base64")
	}

	// 2) preprocess: resize + re-encode 到 5MB 内
	rawOut, _, _, preErr := PreprocessForLqir(rawIn)
	if preErr != nil {
		return nil, errors.Wrap(preErr, "volccv: preprocess failed")
	}

	// 3) re-encode to base64 for body
	b64Out := EncodeImageBase64(rawOut)

	body := CVProcessRequest{
		ReqKey:             cfg.ReqKey,
		BinaryDataBase64:   []string{b64Out},
		ResolutionBoundary: "2k",
		EnableHDR:          false,
		EnableWB:           false,
		ResultFormat:       0, // PNG
		ReturnUrl:          true,
	}

	bytesOut, marshalErr := common.Marshal(body)
	if marshalErr != nil {
		return nil, errors.Wrap(marshalErr, "marshal volccv body")
	}
	return bytes.NewReader(bytesOut), nil
}

// DoRequest 构建并发送签名后的请求到火山 CV 上游
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

// DoResponse lens_lqir 是同步 API：5-10s 返回最终结果。
// 直接把响应包装成 OpenAI-images-edit 风格 ({ created, data: [{ url, width, height }] })，
// c.JSON 返回；不进入 task 表轮询。
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		taskErr = service.TaskErrorWrapper(readErr, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var parsed CVProcessResponse
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		taskErr = service.TaskErrorWrapper(
			errors.Wrapf(err, "body: %s", string(respBody)),
			"unmarshal_response_failed", http.StatusInternalServerError,
		)
		return
	}

	if !isSuccessResponse(&parsed) {
		status, code, msg := translateError(&parsed, resp.StatusCode)
		taskErr = service.TaskErrorWrapper(fmt.Errorf("%s", msg), code, status)
		return
	}

	url := extractResultURL(&parsed)
	b64 := extractResultBase64(&parsed)
	if url == "" && b64 == "" {
		taskErr = service.TaskErrorWrapper(
			fmt.Errorf("no image url/base64 in success response: %s", string(respBody)),
			"invalid_response", http.StatusInternalServerError,
		)
		return
	}

	// 包装成 OpenAI-images-edit 风格响应
	dataItem := OpenAIImagesEditDataItem{}
	if url != "" {
		dataItem.URL = url
	}
	if b64 != "" {
		dataItem.B64 = b64
	}
	// width/height: 火山响应未明确返回；上层（CCS server）需要自行从下载后的图片探测。
	// 这里不强行写 0，留空字段让上层自然处理。

	openaiResp := OpenAIImagesEditResponse{
		Created: common.GetTimestamp(),
		Data:    []OpenAIImagesEditDataItem{dataItem},
	}
	c.JSON(http.StatusOK, openaiResp)

	// taskID/taskData：sync 路径只用于审计，不进任务表
	return parsed.RequestId, respBody, nil
}

// FetchTask sync 模式不轮询，仅满足接口
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	return nil, fmt.Errorf("volccv: lens_lqir is synchronous; FetchTask should not be called")
}

// ParseTaskResult sync 模式不轮询，仅满足接口
func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	return nil, fmt.Errorf("volccv: lens_lqir is synchronous; ParseTaskResult should not be called")
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList()
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

// 显式 import model 防止 lint 报 unused
var _ = model.TaskStatusInProgress

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
