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

	// Playback URL signing config, read from the channel's OtherInfo in Init.
	// Used by the result side (ParseTaskResult) to turn the VOD product
	// FileName into a fully-signed, directly-downloadable http(s) URL so the
	// caller (CCS server) needs ZERO VOD credentials of its own.
	//
	//   playbackDomain  ← OtherInfo.vod_playback_domain  (e.g. "vod.example.com")
	//   playbackScheme  ← OtherInfo.vod_playback_scheme   ("http" | "https", default "https")
	//   urlAuthKey      ← OtherInfo.vod_url_auth_key      (CDN Type-A url-auth key; empty = no signing)
	//
	// Same adaptor instance is reused by the polling loop (service.updateVideoTasks
	// calls Init once per channel, then ParseTaskResult per task), so stashing
	// these on the struct in Init is safe — see service/task_polling.go.
	playbackDomain string
	playbackScheme string
	urlAuthKey     string
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

	// Load playback signing config from the channel's OtherInfo. info.ChannelId
	// is set on both the submit path (RelayInfo.InitChannelMeta) and the polling
	// path (service.updateVideoTasks sets info.ChannelId = channel.Id). A 0/absent
	// channel id or missing OtherInfo simply leaves these empty → result side
	// degrades to an unsigned URL (only valid if the space has url-auth off).
	a.playbackScheme = "https"
	if info.ChannelId > 0 {
		if ch, chErr := model.GetChannelById(info.ChannelId, false); chErr == nil && ch != nil {
			if oi := ch.GetOtherInfo(); oi != nil {
				a.playbackDomain = pickOtherInfoString(oi, "vod_playback_domain", "vodPlaybackDomain")
				if scheme := pickOtherInfoString(oi, "vod_playback_scheme", "vodPlaybackScheme"); scheme == "http" || scheme == "https" {
					a.playbackScheme = scheme
				}
				a.urlAuthKey = pickOtherInfoString(oi, "vod_url_auth_key", "vodUrlAuthKey", "vod_auth_key", "urlAuthKey")
			}
		}
	}
}

// pickOtherInfoString returns the first non-empty string value among keys.
// Mirrors volcengine.pickString (the ASR-side reader) so OtherInfo key names
// stay consistent across the two readers; see relay/channel/volcengine/vod_credentials.go.
func pickOtherInfoString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	var req relaycommon.TaskSubmitReq

	// Two input shapes supported (backwards-compatible):
	//
	//   1. JSON body with `model` + `input_reference: <Vid>` — original path,
	//      CCS server uploads first then submits the Vid here.
	//   2. multipart/form-data with `model` form field + `file` binary part —
	//      this adaptor auto-uploads to VOD and substitutes the resulting
	//      Vid into `input_reference`. Useful when the caller does NOT have
	//      VOD credentials of its own (newapi owns the channel's ak/sk).
	contentType := c.Request.Header.Get("Content-Type")
	isMultipart := strings.Contains(contentType, "multipart/form-data")

	if isMultipart {
		if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
			return wrapTaskError(errors.Wrap(err, "parse multipart form"), "invalid_multipart", http.StatusBadRequest, true)
		}
		req.Model = strings.TrimSpace(c.Request.PostFormValue("model"))
		req.InputReference = strings.TrimSpace(c.Request.PostFormValue("input_reference"))
		// Optional metadata (audioSide etc.) — caller passes as JSON string.
		if metaStr := c.Request.PostFormValue("metadata"); metaStr != "" {
			var meta map[string]interface{}
			if err := common.Unmarshal([]byte(metaStr), &meta); err == nil {
				req.Metadata = meta
			}
		}
	} else {
		if err := common.UnmarshalBodyReusable(c, &req); err != nil {
			return wrapTaskError(err, "invalid_json", http.StatusBadRequest, true)
		}
	}

	if strings.TrimSpace(req.Model) == "" {
		return wrapTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}
	cfg, ok := LookupAction(req.Model)
	if !ok {
		return wrapTaskError(fmt.Errorf("unsupported volcvod model: %s", req.Model), "unsupported_model", http.StatusBadRequest, true)
	}

	// If multipart and no input_reference, auto-upload the `file` part to VOD
	// and substitute the resulting Vid.
	if isMultipart && req.InputReference == "" {
		vid, uploadErr := a.uploadMultipartFileToVOD(c)
		if uploadErr != nil {
			return uploadErr
		}
		req.InputReference = vid
	}

	// VOD 输入必须是 Vid（点播空间内的视频 ID）。Now satisfied by either:
	//   - JSON body input_reference, or
	//   - multipart auto-upload result above.
	if strings.TrimSpace(req.InputReference) == "" {
		return wrapTaskError(fmt.Errorf("input_reference (火山 Vid) is required (provide JSON input_reference or multipart 'file' part)"), "missing_input_reference", http.StatusBadRequest, true)
	}
	// 把 ActionTable 的 TaskType 暂存到 info.Action（复用 newapi 现有约定）
	info.Action = cfg.TaskType
	c.Set("task_request", req)
	c.Set("volcvod_action", cfg) // 缓存 cfg 给 BuildRequest* 用
	return nil
}

// uploadMultipartFileToVOD reads the `file` part from the multipart form and
// uploads it to Volcengine VOD using the channel's ak/sk. Returns the new Vid
// or a wrapped TaskError suitable for ValidateRequestAndSetAction.
//
// The VOD `SpaceName` is read from an optional `space` form field, falling
// back to the channel's `OtherInfo.vod_space`. We require it explicitly
// because there's no "default" space in Volcengine VOD.
func (a *TaskAdaptor) uploadMultipartFileToVOD(c *gin.Context) (string, *dto.TaskError) {
	if a.accessKeyId == "" || a.accessKeySecret == "" {
		return "", wrapTaskError(fmt.Errorf("volcvod: channel api_key must be '<AccessKeyId>:<AccessKeySecret>' to support multipart upload"), "invalid_channel_key", http.StatusBadRequest, true)
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		return "", wrapTaskError(errors.Wrap(err, "read multipart 'file' part"), "missing_file_part", http.StatusBadRequest, true)
	}
	defer file.Close()

	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, file); err != nil {
		return "", wrapTaskError(errors.Wrap(err, "copy 'file' part"), "read_file_failed", http.StatusInternalServerError, false)
	}

	filename := ""
	if header != nil {
		filename = header.Filename
	}

	// SpaceName: form field 'space' wins; otherwise channel.OtherInfo.vod_space.
	space := strings.TrimSpace(c.Request.PostFormValue("space"))
	if space == "" {
		// Best-effort: fetch the channel record to read OtherInfo.vod_space.
		ch, chErr := model.GetChannelById(c.GetInt("channel_id"), false)
		if chErr == nil && ch != nil {
			oi := ch.GetOtherInfo()
			if oi != nil {
				if v, ok := oi["vod_space"].(string); ok {
					space = strings.TrimSpace(v)
				}
				if space == "" {
					if v, ok := oi["vodSpace"].(string); ok {
						space = strings.TrimSpace(v)
					}
				}
			}
		}
	}
	if space == "" {
		return "", wrapTaskError(fmt.Errorf("volcvod multipart upload requires 'space' form field or channel OtherInfo.vod_space"), "missing_space", http.StatusBadRequest, true)
	}

	region := Region // "cn-north-1"
	opts := VodUploadOpts{
		SpaceName:     space,
		Region:        region,
		AccessKey:     a.accessKeyId,
		SecretKey:     a.accessKeySecret,
		FileName:      filename,
		FileExtension: InferExtFromFilename(filename),
	}
	contentType := InferContentTypeFromFilename(filename)

	res, uploadErr := UploadMediaToVOD(buf.Bytes(), opts, contentType)
	if uploadErr != nil {
		return "", wrapTaskError(uploadErr, "vod_upload_failed", http.StatusBadGateway, false)
	}
	return res.Vid, nil
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
		// 我们用约定的私有 scheme 编码所有信息，CCS server 端 volcvod driver 用
		// URL parser 反向解析。
		//
		// 新契约（newapi 自带签名，CCS 零 VOD 凭据，直接下载签名 URL）：
		//   Erase:        vod-result-erase://?video=<urlencoded signed http url>&duration=<s>
		//   AudioExtract: vod-result-audio://?voice=<urlencoded signed>&bg=<urlencoded signed>&duration=<s>
		//
		// 签名所需 domain / scheme / url_auth_key 来自 channel OtherInfo（Init 阶段读入）。
		// 若 OtherInfo 未配 playback domain，则退回旧的 vod-erase:// / vod-audio:// 编码
		// （由持有 VOD 凭据的旧版 CCS 自行签名下载），保持向后兼容。
		encoded, encErr := a.encodeSignedVodResultUrl(r.Output.Task)
		if encErr != nil {
			return nil, errors.Wrap(encErr, "encode signed vod result url")
		}
		result.Url = encoded
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

// urlAuthSignTTLSeconds is the validity window for the playback auth_key we sign
// into result URLs. 3600s gives CCS ample time to poll-then-download.
const urlAuthSignTTLSeconds = 3600

// encodeSignedVodResultUrl turns the VOD product FileName(s) into fully-signed,
// directly-downloadable http(s) URL(s) and encodes them into a private scheme
// the CCS server parses. This lets CCS download the product with ZERO VOD
// credentials of its own — newapi owns the channel ak/sk + url-auth key.
//
// New contract:
//
//	Erase:        vod-result-erase://?video=<urlencoded signed url>&duration=<seconds>
//	AudioExtract: vod-result-audio://?voice=<urlencoded signed>&bg=<urlencoded signed>&duration=<s>
//
// When the channel has no playback domain configured (a.playbackDomain == ""),
// we cannot build a downloadable URL here, so we fall back to the LEGACY
// encodeVodResultUrl (vod-erase:// / vod-audio://) which carries the raw
// FileName + Vid for a credential-holding CCS to sign itself. This keeps the
// rollout backwards-compatible: configure OtherInfo.vod_playback_domain on the
// channel to flip on the zero-credential path.
//
// When playbackDomain is set but urlAuthKey is empty (space has url-auth off),
// signVodPlaybackURL returns the URL unsigned — still directly downloadable.
func (a *TaskAdaptor) encodeSignedVodResultUrl(task OutputTaskSpec) (string, error) {
	if a.playbackDomain == "" {
		// No domain → keep legacy behaviour for credential-holding callers.
		return encodeVodResultUrl(task), nil
	}

	sign := func(fileName string) (string, error) {
		raw := buildPlaybackURL(a.playbackScheme, a.playbackDomain, fileName)
		return signVodPlaybackURL(raw, a.urlAuthKey, urlAuthSignTTLSeconds)
	}

	if task.Erase != nil {
		signedVideo, err := sign(task.Erase.File.FileName)
		if err != nil {
			return "", errors.Wrap(err, "sign erase video url")
		}
		q := url.Values{}
		q.Set("video", signedVideo)
		if task.Erase.Duration > 0 {
			q.Set("duration", fmt.Sprintf("%v", task.Erase.Duration))
		}
		return "vod-result-erase://?" + q.Encode(), nil
	}

	if task.AudioExtract != nil {
		signedVoice, err := sign(task.AudioExtract.Voice.FileName)
		if err != nil {
			return "", errors.Wrap(err, "sign audio voice url")
		}
		signedBg, err := sign(task.AudioExtract.Background.FileName)
		if err != nil {
			return "", errors.Wrap(err, "sign audio background url")
		}
		q := url.Values{}
		q.Set("voice", signedVoice)
		q.Set("bg", signedBg)
		if task.AudioExtract.Duration > 0 {
			q.Set("duration", fmt.Sprintf("%v", task.AudioExtract.Duration))
		}
		return "vod-result-audio://?" + q.Encode(), nil
	}

	return "", nil
}

// encodeVodResultUrl 把 Output.Task 信息编码为 CCS server 能解析的 URL
//
// LEGACY 编码：仅在 channel 未配 playback domain 时作为兜底（持 VOD 凭据的旧版 CCS
// 自行签名下载）。新的零凭据路径见 encodeSignedVodResultUrl。
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
