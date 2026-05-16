package viapi

// ViapiSubmitResponse 异步任务提交后返回的响应（如 EraseVideoSubtitles / SuperResolveVideo）
type ViapiSubmitResponse struct {
	RequestId string             `json:"RequestId,omitempty"`
	Data      ViapiSubmitData    `json:"Data,omitempty"`
	Code      string             `json:"Code,omitempty"`
	Message   string             `json:"Message,omitempty"`
	HostId    string             `json:"HostId,omitempty"`
	Recommend string             `json:"Recommend,omitempty"`
}

// ViapiSubmitData 提交响应的 Data 字段
// 异步：包含 JobId；同步：直接包含结果字段（如 ImageURL）
type ViapiSubmitData struct {
	JobId string `json:"JobId,omitempty"`
	// 同步类 Action 的常见结果字段
	ImageURL string `json:"ImageURL,omitempty"`
	VideoURL string `json:"VideoURL,omitempty"`
	// 通用结果，由具体 Action 决定（如 mask urls / segments 等）
	Elements []map[string]interface{} `json:"Elements,omitempty"`
}

// ViapiQueryResponse 异步任务查询 GetAsyncJobResult 响应
type ViapiQueryResponse struct {
	RequestId string         `json:"RequestId,omitempty"`
	Data      ViapiQueryData `json:"Data,omitempty"`
	Code      string         `json:"Code,omitempty"`
	Message   string         `json:"Message,omitempty"`
}

// ViapiQueryData 查询响应的 Data 字段
type ViapiQueryData struct {
	JobId        string `json:"JobId,omitempty"`
	Status       string `json:"Status,omitempty"` // PROCESSING / SUCCESS / FAIL
	Result       string `json:"Result,omitempty"` // JSON 字符串（内层 schema 因 Action 而异）
	ErrorCode    string `json:"ErrorCode,omitempty"`
	ErrorMessage string `json:"ErrorMessage,omitempty"`
}

// ViapiResultPayload 查询响应中 Result 字段反序列化后的通用 payload
// 不同 Action 取不同字段：视频类用 VideoURL，图像类用 ImageURL/Elements
type ViapiResultPayload struct {
	VideoURL string                   `json:"VideoURL,omitempty"`
	ImageURL string                   `json:"ImageURL,omitempty"`
	Elements []map[string]interface{} `json:"Elements,omitempty"`
}
