package fastaitoken

// requestPayload 是 fastaitoken 视频任务的上游请求体。
// 画布的异步任务字段和 FastAI 文档的顶层视频字段不同，必须在这里完成摊平。
type requestPayload struct {
	Model         string   `json:"model"`
	Prompt        string   `json:"prompt"`
	Mode          string   `json:"mode,omitempty"`
	Resolution    string   `json:"resolution,omitempty"`
	Ratio         string   `json:"ratio,omitempty"`
	Duration      int      `json:"duration,omitempty"`
	Images        []string `json:"images,omitempty"`
	Videos        []string `json:"videos,omitempty"`
	Audio         []string `json:"audio,omitempty"`
	GenerateAudio *bool    `json:"generate_audio,omitempty"`
	Watermark     *bool    `json:"watermark,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
}

// taskMetadata 是画布两个 driver 塞进 metadata 的字段并集：
// seedance driver 给 resolution/ratio/generate_audio/watermark/content[]，
// minimax-h3 driver 给 ratio/aigc_watermark/first_frame_image/reference_* 等。
type taskMetadata struct {
	Resolution      string   `json:"resolution,omitempty"`
	Ratio           string   `json:"ratio,omitempty"`
	GenerateAudio   *bool    `json:"generate_audio,omitempty"`
	Watermark       *bool    `json:"watermark,omitempty"`
	AigcWatermark   *bool    `json:"aigc_watermark,omitempty"`
	Seed            *int     `json:"seed,omitempty"`
	Content         []any    `json:"content,omitempty"`
	FirstFrameImage string   `json:"first_frame_image,omitempty"`
	LastFrameImage  string   `json:"last_frame_image,omitempty"`
	ReferenceImages []string `json:"reference_images,omitempty"`
	ReferenceVideos []string `json:"reference_videos,omitempty"`
	ReferenceAudios []string `json:"reference_audios,omitempty"`
}

// submitResponse 提交成功的响应（HTTP 202）：
// {"id":"...","task_id":"...","object":"video.generation.task","status":"processing","progress":3}
type submitResponse struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// fetchResponse 轮询响应。成功态实测样本：
//
//	{"id":"...","task_id":"...","object":"video.generation.task","status":"completed","progress":100,
//	 "usage":{"total_tokens":87277,"completion_tokens":87277},
//	 "video_url":"https://...","result_url":"https://...","download_url":"https://..."}
//
// 三个 URL 字段实测同值；视频地址在**顶层**，不在 content 下。
type fetchResponse struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
	VideoURL   string `json:"video_url"`
	ResultURL  string `json:"result_url"`
	FailReason string `json:"fail_reason"`
	Usage      struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *fetchResponse) videoURL() string {
	if r.VideoURL != "" {
		return r.VideoURL
	}
	return r.ResultURL
}

func (r *fetchResponse) failReason() string {
	if r.Error != nil && r.Error.Message != "" {
		return r.Error.Message
	}
	if r.FailReason != "" {
		return r.FailReason
	}
	return "task failed"
}
