package hailuo

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

// https://platform.minimaxi.com/docs/api-reference/video-generation-intro
type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType          int
	apiKey               string
	baseURL              string
	taskEndpointOverride *dto.TaskEndpointOverride
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
	a.taskEndpointOverride = info.ChannelOtherSettings.TaskEndpointOverride
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.taskEndpointOverride != nil && strings.TrimSpace(a.taskEndpointOverride.SubmitPath) != "" {
		return taskcommon.ResolveEndpointURL(a.baseURL, a.taskEndpointOverride.SubmitPath, nil)
	}
	if isH3Model(info.UpstreamModelName) {
		return taskcommon.ResolveEndpointURL(a.baseURL, H3CreateEndpoint, nil)
	}
	return fmt.Sprintf("%s%s", a.baseURL, TextToVideoEndpoint), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("invalid request type in context")
	}

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	if isH3Model(info.UpstreamModelName) {
		return a.doH3Response(c, resp.StatusCode, responseBody, info)
	}

	var hResp VideoResponse
	if err := common.Unmarshal(responseBody, &hResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if hResp.BaseResp.StatusCode != StatusSuccess {
		taskErr = service.TaskErrorWrapper(
			fmt.Errorf("hailuo api error: %s", hResp.BaseResp.StatusMsg),
			strconv.Itoa(hResp.BaseResp.StatusCode),
			http.StatusBadRequest,
		)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return hResp.TaskID, responseBody, nil
}

func (a *TaskAdaptor) DoErrorResponse(_ *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) *dto.TaskError {
	if resp == nil || !isH3Model(info.UpstreamModelName) {
		return nil
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	return parseH3CreateError(resp.StatusCode, responseBody)
}

func (a *TaskAdaptor) doH3Response(c *gin.Context, statusCode int, responseBody []byte, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	var hResp H3CreateResponse
	if err := common.Unmarshal(responseBody, &hResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}
	if hResp.Error != nil || statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", nil, taskErrorFromH3Response(statusCode, hResp)
	}
	taskID = hResp.GetTaskID()
	if taskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("MiniMax-H3 response missing task_id"), "missing_task_id", http.StatusBadGateway)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return taskID, responseBody, nil
}

func parseH3CreateError(statusCode int, responseBody []byte) *dto.TaskError {
	var response H3CreateResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return service.TaskErrorWrapper(
			fmt.Errorf("MiniMax-H3 upstream returned HTTP %d", statusCode),
			"minimax_h3_error",
			statusCode,
		)
	}
	return taskErrorFromH3Response(statusCode, response)
}

func taskErrorFromH3Response(statusCode int, response H3CreateResponse) *dto.TaskError {
	message := "MiniMax-H3 upstream returned an error"
	code := "minimax_h3_error"
	if response.Error != nil {
		if value := strings.TrimSpace(response.Error.Message); value != "" {
			message = value
		}
		if value := strings.TrimSpace(response.Error.Type); value != "" {
			code = value
		}
		statusCode = response.Error.StatusCode(statusCode)
	}
	if statusCode < http.StatusBadRequest || statusCode > 599 {
		statusCode = http.StatusBadGateway
	}
	taskErr := service.TaskErrorWrapper(fmt.Errorf("%s", message), code, statusCode)
	if requestID := strings.TrimSpace(response.RequestID); requestID != "" {
		taskErr.Data = map[string]string{"request_id": requestID}
	}
	return taskErr
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	modelName, _ := body["model"].(string)
	uri, err := a.buildFetchURL(baseUrl, taskID, modelName)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) buildFetchURL(baseURL, taskID, modelName string) (string, error) {
	if a.taskEndpointOverride != nil && strings.TrimSpace(a.taskEndpointOverride.FetchPath) != "" {
		return taskcommon.ResolveEndpointURL(baseURL, a.taskEndpointOverride.FetchPath, map[string]string{
			"task_id": taskID,
		})
	}
	if isH3Model(modelName) {
		return taskcommon.ResolveEndpointURL(baseURL, H3QueryEndpoint, map[string]string{
			"task_id": taskID,
		})
	}
	return fmt.Sprintf("%s%s?task_id=%s", baseURL, QueryTaskEndpoint, taskID), nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (any, error) {
	if isH3Model(info.UpstreamModelName) {
		return buildH3Request(req, info.UpstreamModelName)
	}
	return a.convertToLegacyRequestPayload(req, info)
}

func (a *TaskAdaptor) convertToLegacyRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*VideoRequest, error) {
	modelConfig := GetModelConfig(info.UpstreamModelName)
	duration := DefaultDuration
	if req.Duration > 0 {
		duration = req.Duration
	}
	resolution := modelConfig.DefaultResolution
	if req.Size != "" {
		resolution = a.parseResolutionFromSize(req.Size, modelConfig)
	}

	videoRequest := &VideoRequest{
		Model:      info.UpstreamModelName,
		Prompt:     req.Prompt,
		Duration:   &duration,
		Resolution: resolution,
	}
	if err := req.UnmarshalMetadata(&videoRequest); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata to video request failed")
	}
	videoRequest.Model = info.UpstreamModelName

	return videoRequest, nil
}

func (a *TaskAdaptor) parseResolutionFromSize(size string, modelConfig ModelConfig) string {
	switch {
	case strings.Contains(strings.ToUpper(size), Resolution2K):
		return Resolution2K
	case strings.Contains(size, "1080"):
		return Resolution1080P
	case strings.Contains(size, "768"):
		return Resolution768P
	case strings.Contains(size, "720"):
		return Resolution720P
	case strings.Contains(size, "512"):
		return Resolution512P
	default:
		return modelConfig.DefaultResolution
	}
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	if result, recognized, err := parseH3TaskResult(respBody); recognized || err != nil {
		return result, err
	}

	resTask := QueryTaskResponse{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{}

	if resTask.BaseResp.StatusCode == StatusSuccess {
		taskResult.Code = 0
	} else {
		taskResult.Code = resTask.BaseResp.StatusCode
		taskResult.Reason = resTask.BaseResp.StatusMsg
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
	}

	switch resTask.Status {
	case TaskStatusPreparing, TaskStatusQueueing, TaskStatusProcessing:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
		if resTask.Status == TaskStatusProcessing {
			taskResult.Progress = "50%"
		}
	case TaskStatusSuccess:
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = a.buildVideoURL(resTask.TaskID, resTask.FileID)
	case TaskStatusFailed:
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		if taskResult.Reason == "" {
			taskResult.Reason = "task failed"
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func parseH3TaskResult(respBody []byte) (*relaycommon.TaskInfo, bool, error) {
	var response H3QueryResponse
	if err := common.Unmarshal(respBody, &response); err != nil {
		return nil, false, nil
	}
	task := response.GetTask()
	if task == nil {
		if response.Error != nil || strings.EqualFold(response.Type, "error") {
			reason := responseErrorMessage(response.Error)
			if reason == "" {
				reason = "MiniMax-H3 upstream returned an error"
			}
			return &relaycommon.TaskInfo{
				Status:   model.TaskStatusFailure,
				Progress: "100%",
				Reason:   reason,
			}, true, nil
		}
		return nil, false, nil
	}

	result := &relaycommon.TaskInfo{TaskID: decodeH3ID(task.ID)}
	switch strings.ToLower(strings.TrimSpace(task.Status)) {
	case "queued":
		result.Status = model.TaskStatusInProgress
		result.Progress = "10%"
	case "running", "processing", "in_progress":
		result.Status = model.TaskStatusInProgress
		result.Progress = "50%"
	case "succeeded", "success", "completed":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		result.Url = firstNonEmpty(task.Content.URL, task.Content.VideoURL, task.Output.URL, task.Output.VideoURL)
	case "failed", "failure", "cancelled", "canceled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = firstNonEmpty(responseErrorMessage(task.Error), task.FailReason, task.FailureReason, task.Message, "task failed")
	default:
		result.Status = model.TaskStatusInProgress
		result.Progress = "30%"
	}
	return result, true, nil
}

func responseErrorMessage(detail *H3ErrorDetail) string {
	if detail == nil {
		return ""
	}
	return strings.TrimSpace(detail.Message)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	if isH3Model(originTask.Properties.UpstreamModelName) {
		openAIVideo := originTask.ToOpenAIVideo()
		var h3Resp H3QueryResponse
		if err := common.Unmarshal(originTask.Data, &h3Resp); err == nil {
			if task := h3Resp.GetTask(); task != nil && task.Error != nil {
				openAIVideo.Error = &dto.OpenAIVideoError{
					Message: task.Error.Message,
					Code:    task.Error.Type,
				}
			} else if h3Resp.Error != nil {
				openAIVideo.Error = &dto.OpenAIVideoError{
					Message: h3Resp.Error.Message,
					Code:    h3Resp.Error.Type,
				}
			}
		}
		jsonData, err := common.Marshal(openAIVideo)
		if err != nil {
			return nil, errors.Wrap(err, "marshal openai video failed")
		}
		return jsonData, nil
	}

	var hailuoResp QueryTaskResponse
	if err := common.Unmarshal(originTask.Data, &hailuoResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal hailuo task data failed")
	}

	openAIVideo := originTask.ToOpenAIVideo()
	if hailuoResp.BaseResp.StatusCode != StatusSuccess {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: hailuoResp.BaseResp.StatusMsg,
			Code:    strconv.Itoa(hailuoResp.BaseResp.StatusCode),
		}
	}

	jsonData, err := common.Marshal(openAIVideo)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai video failed")
	}

	return jsonData, nil
}

func (a *TaskAdaptor) buildVideoURL(_, fileID string) string {
	if a.apiKey == "" || a.baseURL == "" {
		return ""
	}

	url := fmt.Sprintf("%s/v1/files/retrieve?file_id=%s", a.baseURL, fileID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return ""
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var retrieveResp RetrieveFileResponse
	if err := common.Unmarshal(responseBody, &retrieveResp); err != nil {
		return ""
	}

	if retrieveResp.BaseResp.StatusCode != StatusSuccess {
		return ""
	}

	return retrieveResp.File.DownloadURL
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsInt(slice []int, item int) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// EstimateBilling 根据请求的 resolution + duration 算出档位倍率。
// H3 quota = 0.20 元/秒基价 × duration × resolution ratio × group ratio。
// 旧 Hailuo 模型继续使用各自既有 tier ratio。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	if isH3Model(info.UpstreamModelName) {
		duration := taskReq.Duration
		if duration == 0 {
			duration = H3DefaultDuration
		}
		resolution, err := normalizeH3Resolution(taskReq.Size)
		if err != nil || duration < 4 || duration > 15 {
			return nil
		}
		resolutionRatio := 1.0
		if resolution == Resolution2K {
			resolutionRatio = H3HighResolutionRatio
		}
		return map[string]float64{
			"hailuo-h3-duration":   float64(duration),
			"hailuo-h3-resolution": resolutionRatio,
		}
	}

	modelConfig := GetModelConfig(info.UpstreamModelName)
	duration := DefaultDuration
	if taskReq.Duration > 0 {
		duration = taskReq.Duration
	}
	resolution := modelConfig.DefaultResolution
	if taskReq.Size != "" {
		resolution = a.parseResolutionFromSize(taskReq.Size, modelConfig)
	}

	tierKey := fmt.Sprintf("%s-%d", resolution, duration)
	modelTiers, ok := HailuoTierRatios[info.UpstreamModelName]
	if !ok {
		return nil
	}
	ratio, ok := modelTiers[tierKey]
	if !ok {
		return nil
	}
	return map[string]float64{
		fmt.Sprintf("hailuo-tier-%s", tierKey): ratio,
	}
}
