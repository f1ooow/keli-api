package hailuo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/relay/common"
)

type SubjectReference struct {
	Type  string   `json:"type"`  // Subject type, currently only supports "character"
	Image []string `json:"image"` // Array of subject reference images (currently only supports single image)
}

type VideoRequest struct {
	Model            string             `json:"model"`
	Prompt           string             `json:"prompt,omitempty"`
	PromptOptimizer  *bool              `json:"prompt_optimizer,omitempty"`
	FastPretreatment *bool              `json:"fast_pretreatment,omitempty"`
	Duration         *int               `json:"duration,omitempty"`
	Resolution       string             `json:"resolution,omitempty"`
	CallbackURL      string             `json:"callback_url,omitempty"`
	AigcWatermark    *bool              `json:"aigc_watermark,omitempty"`
	FirstFrameImage  string             `json:"first_frame_image,omitempty"` // For image-to-video and start-end-to-video
	LastFrameImage   string             `json:"last_frame_image,omitempty"`  // For start-end-to-video
	SubjectReference []SubjectReference `json:"subject_reference,omitempty"` // For subject-reference-to-video
}

type H3Metadata struct {
	Ratio           string   `json:"ratio,omitempty"`
	CallbackURL     string   `json:"callback_url,omitempty"`
	AigcWatermark   *bool    `json:"aigc_watermark,omitempty"`
	FirstFrameImage string   `json:"first_frame_image,omitempty"`
	LastFrameImage  string   `json:"last_frame_image,omitempty"`
	ReferenceImages []string `json:"reference_images,omitempty"`
	ReferenceVideos []string `json:"reference_videos,omitempty"`
	ReferenceAudios []string `json:"reference_audios,omitempty"`
}

type H3MediaURL struct {
	URL string `json:"url"`
}

type H3ContentItem struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	ImageURL *H3MediaURL `json:"image_url,omitempty"`
	VideoURL *H3MediaURL `json:"video_url,omitempty"`
	AudioURL *H3MediaURL `json:"audio_url,omitempty"`
	Role     string      `json:"role,omitempty"`
}

type H3VideoRequest struct {
	Model         string          `json:"model"`
	Content       []H3ContentItem `json:"content"`
	Resolution    string          `json:"resolution"`
	Duration      int             `json:"duration"`
	Ratio         string          `json:"ratio,omitempty"`
	CallbackURL   string          `json:"callback_url,omitempty"`
	AigcWatermark *bool           `json:"aigc_watermark,omitempty"`
}

type H3ErrorDetail struct {
	Type     string `json:"type,omitempty"`
	Message  string `json:"message,omitempty"`
	HTTPCode string `json:"http_code,omitempty"`
}

type H3CreateResult struct {
	TaskID json.RawMessage `json:"task_id,omitempty"`
}

type H3CreateResponse struct {
	Type      string          `json:"type,omitempty"`
	TaskID    json.RawMessage `json:"task_id,omitempty"`
	Result    *H3CreateResult `json:"result,omitempty"`
	Error     *H3ErrorDetail  `json:"error,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
}

type H3TaskContent struct {
	URL          string `json:"url,omitempty"`
	VideoURL     string `json:"video_url,omitempty"`
	LastFrameURL string `json:"last_frame_url,omitempty"`
}

type H3Task struct {
	ID            json.RawMessage `json:"id,omitempty"`
	Status        string          `json:"status,omitempty"`
	Content       H3TaskContent   `json:"content,omitempty"`
	Output        H3TaskContent   `json:"output,omitempty"`
	Error         *H3ErrorDetail  `json:"error,omitempty"`
	FailReason    string          `json:"fail_reason,omitempty"`
	FailureReason string          `json:"failure_reason,omitempty"`
	Message       string          `json:"message,omitempty"`
}

type H3QueryResult struct {
	Task *H3Task `json:"task,omitempty"`
	H3Task
}

type H3QueryResponse struct {
	Type      string         `json:"type,omitempty"`
	Task      *H3Task        `json:"task,omitempty"`
	Result    *H3QueryResult `json:"result,omitempty"`
	Error     *H3ErrorDetail `json:"error,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

func (r H3QueryResponse) GetTask() *H3Task {
	if r.Task != nil {
		return r.Task
	}
	if r.Result == nil {
		return nil
	}
	if r.Result.Task != nil {
		return r.Result.Task
	}
	if strings.TrimSpace(r.Result.Status) != "" {
		return &r.Result.H3Task
	}
	return nil
}

func decodeH3ID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	return strings.Trim(strings.TrimSpace(string(raw)), "\"")
}

func (r H3CreateResponse) GetTaskID() string {
	if taskID := decodeH3ID(r.TaskID); taskID != "" {
		return taskID
	}
	if r.Result != nil {
		return decodeH3ID(r.Result.TaskID)
	}
	return ""
}

func (e *H3ErrorDetail) StatusCode(fallback int) int {
	if e == nil {
		return fallback
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(e.HTTPCode)); err == nil && parsed >= 400 && parsed <= 599 {
		return parsed
	}
	return fallback
}

func isH3Model(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), ModelMiniMaxH3)
}

func normalizeH3Resolution(size string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(size)) {
	case "", Resolution768P:
		return Resolution768P, nil
	case Resolution2K:
		return Resolution2K, nil
	default:
		return "", fmt.Errorf("MiniMax-H3 only supports resolution %s or %s", Resolution768P, Resolution2K)
	}
}

func validateH3MediaList(name string, values []string, max int) error {
	if len(values) > max {
		return fmt.Errorf("MiniMax-H3 supports at most %d %s", max, name)
	}
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("MiniMax-H3 %s[%d] must not be empty", name, index)
		}
	}
	return nil
}

func buildH3Request(req *common.TaskSubmitReq, model string) (*H3VideoRequest, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("MiniMax-H3 prompt must not be empty")
	}
	if utf8.RuneCountInString(prompt) > 7000 {
		return nil, fmt.Errorf("MiniMax-H3 prompt must not exceed 7000 characters")
	}

	duration := req.Duration
	if duration == 0 {
		duration = H3DefaultDuration
	}
	if duration < 4 || duration > 15 {
		return nil, fmt.Errorf("MiniMax-H3 duration must be an integer between 4 and 15 seconds")
	}
	resolution, err := normalizeH3Resolution(req.Size)
	if err != nil {
		return nil, err
	}

	metadata := H3Metadata{}
	if err := req.UnmarshalMetadata(&metadata); err != nil {
		return nil, err
	}
	if err := validateH3MediaList("reference images", metadata.ReferenceImages, 9); err != nil {
		return nil, err
	}
	if err := validateH3MediaList("reference videos", metadata.ReferenceVideos, 3); err != nil {
		return nil, err
	}
	if err := validateH3MediaList("reference audios", metadata.ReferenceAudios, 3); err != nil {
		return nil, err
	}

	firstFrame := strings.TrimSpace(metadata.FirstFrameImage)
	lastFrame := strings.TrimSpace(metadata.LastFrameImage)
	hasFrames := firstFrame != "" || lastFrame != ""
	hasReferences := len(metadata.ReferenceImages)+len(metadata.ReferenceVideos)+len(metadata.ReferenceAudios) > 0
	if hasFrames && hasReferences {
		return nil, fmt.Errorf("MiniMax-H3 frame inputs cannot be mixed with reference media")
	}
	if strings.EqualFold(strings.TrimSpace(req.Mode), "all_ref") && !hasReferences {
		return nil, fmt.Errorf("MiniMax-H3 all_ref mode requires at least one reference image, video, or audio")
	}

	content := []H3ContentItem{{Type: "text", Text: prompt}}
	if firstFrame != "" {
		content = append(content, H3ContentItem{
			Type: "image_url", ImageURL: &H3MediaURL{URL: firstFrame}, Role: "first_frame",
		})
	}
	if lastFrame != "" {
		content = append(content, H3ContentItem{
			Type: "image_url", ImageURL: &H3MediaURL{URL: lastFrame}, Role: "last_frame",
		})
	}
	for _, value := range metadata.ReferenceImages {
		content = append(content, H3ContentItem{
			Type: "image_url", ImageURL: &H3MediaURL{URL: strings.TrimSpace(value)}, Role: "reference_image",
		})
	}
	for _, value := range metadata.ReferenceVideos {
		content = append(content, H3ContentItem{
			Type: "video_url", VideoURL: &H3MediaURL{URL: strings.TrimSpace(value)}, Role: "reference_video",
		})
	}
	for _, value := range metadata.ReferenceAudios {
		content = append(content, H3ContentItem{
			Type: "audio_url", AudioURL: &H3MediaURL{URL: strings.TrimSpace(value)}, Role: "reference_audio",
		})
	}

	allowedRatios := map[string]bool{
		"adaptive": true,
		"21:9":     true,
		"16:9":     true,
		"4:3":      true,
		"1:1":      true,
		"3:4":      true,
		"9:16":     true,
	}
	ratio := strings.TrimSpace(metadata.Ratio)
	if hasFrames {
		ratio = "adaptive"
	} else if hasReferences {
		if ratio == "" {
			ratio = "adaptive"
		}
	} else if ratio == "" || strings.EqualFold(ratio, "adaptive") {
		return nil, fmt.Errorf("MiniMax-H3 text-to-video requires a non-adaptive ratio")
	}
	if !allowedRatios[ratio] {
		return nil, fmt.Errorf("MiniMax-H3 does not support ratio %s", ratio)
	}

	return &H3VideoRequest{
		Model:         model,
		Content:       content,
		Resolution:    resolution,
		Duration:      duration,
		Ratio:         ratio,
		CallbackURL:   strings.TrimSpace(metadata.CallbackURL),
		AigcWatermark: metadata.AigcWatermark,
	}, nil
}

type VideoResponse struct {
	TaskID   string   `json:"task_id"`
	BaseResp BaseResp `json:"base_resp"`
}

type BaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type QueryTaskRequest struct {
	TaskID string `json:"task_id"`
}

type QueryTaskResponse struct {
	TaskID      string   `json:"task_id"`
	Status      string   `json:"status"`
	FileID      string   `json:"file_id,omitempty"`
	VideoWidth  int      `json:"video_width,omitempty"`
	VideoHeight int      `json:"video_height,omitempty"`
	BaseResp    BaseResp `json:"base_resp"`
}

type ErrorInfo struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type TaskStatusInfo struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	FileID    string `json:"file_id,omitempty"`
	VideoURL  string `json:"video_url,omitempty"`
	ErrorCode int    `json:"error_code,omitempty"`
	ErrorMsg  string `json:"error_msg,omitempty"`
}

type ModelConfig struct {
	Name                 string
	DefaultResolution    string
	SupportedDurations   []int
	SupportedResolutions []string
	HasPromptOptimizer   bool
	HasFastPretreatment  bool
}

type RetrieveFileResponse struct {
	File     FileObject `json:"file"`
	BaseResp BaseResp   `json:"base_resp"`
}

type FileObject struct {
	FileID      int64  `json:"file_id"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	Filename    string `json:"filename"`
	Purpose     string `json:"purpose"`
	DownloadURL string `json:"download_url"`
}

func GetModelConfig(model string) ModelConfig {
	configs := map[string]ModelConfig{
		ModelMiniMaxH3: {
			Name:                 ModelMiniMaxH3,
			DefaultResolution:    Resolution768P,
			SupportedDurations:   []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
			SupportedResolutions: []string{Resolution768P, Resolution2K},
			HasPromptOptimizer:   false,
			HasFastPretreatment:  false,
		},
		"MiniMax-Hailuo-2.3": {
			Name:                 "MiniMax-Hailuo-2.3",
			DefaultResolution:    Resolution768P,
			SupportedDurations:   []int{6, 10},
			SupportedResolutions: []string{Resolution768P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  true,
		},
		"MiniMax-Hailuo-2.3-Fast": {
			Name:                 "MiniMax-Hailuo-2.3-Fast",
			DefaultResolution:    Resolution768P,
			SupportedDurations:   []int{6, 10},
			SupportedResolutions: []string{Resolution768P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  true,
		},
		"MiniMax-Hailuo-02": {
			Name:                 "MiniMax-Hailuo-02",
			DefaultResolution:    Resolution768P,
			SupportedDurations:   []int{6, 10},
			SupportedResolutions: []string{Resolution512P, Resolution768P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  true,
		},
		"T2V-01-Director": {
			Name:                 "T2V-01-Director",
			DefaultResolution:    Resolution768P,
			SupportedDurations:   []int{6},
			SupportedResolutions: []string{Resolution768P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  false,
		},
		"T2V-01": {
			Name:                 "T2V-01",
			DefaultResolution:    Resolution720P,
			SupportedDurations:   []int{6},
			SupportedResolutions: []string{Resolution720P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  false,
		},
		"I2V-01-Director": {
			Name:                 "I2V-01-Director",
			DefaultResolution:    Resolution720P,
			SupportedDurations:   []int{6},
			SupportedResolutions: []string{Resolution720P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  false,
		},
		"I2V-01-live": {
			Name:                 "I2V-01-live",
			DefaultResolution:    Resolution720P,
			SupportedDurations:   []int{6},
			SupportedResolutions: []string{Resolution720P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  false,
		},
		"I2V-01": {
			Name:                 "I2V-01",
			DefaultResolution:    Resolution720P,
			SupportedDurations:   []int{6},
			SupportedResolutions: []string{Resolution720P, Resolution1080P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  false,
		},
		"S2V-01": {
			Name:                 "S2V-01",
			DefaultResolution:    Resolution720P,
			SupportedDurations:   []int{6},
			SupportedResolutions: []string{Resolution720P},
			HasPromptOptimizer:   true,
			HasFastPretreatment:  false,
		},
	}

	if config, exists := configs[model]; exists {
		return config
	}

	return ModelConfig{
		Name:                 model,
		DefaultResolution:    DefaultResolution,
		SupportedDurations:   []int{6},
		SupportedResolutions: []string{DefaultResolution},
		HasPromptOptimizer:   true,
		HasFastPretreatment:  false,
	}
}
