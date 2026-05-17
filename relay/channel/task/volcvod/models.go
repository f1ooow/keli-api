package volcvod

// =====================================================
// StartExecution 请求 body
// =====================================================

// StartExecutionRequest 提交媒体处理任务的统一 body schema
type StartExecutionRequest struct {
	Input     InputSpec     `json:"Input"`
	Operation OperationSpec `json:"Operation"`
}

// InputSpec 输入文件描述
type InputSpec struct {
	Type      string `json:"Type"`                // "Vid" | "DirectUrl"
	Vid       string `json:"Vid,omitempty"`       // 当 Type=Vid
	DirectUrl string `json:"DirectUrl,omitempty"` // 当 Type=DirectUrl
}

// OperationSpec 操作描述
type OperationSpec struct {
	Type string   `json:"Type"` // "Task"
	Task TaskSpec `json:"Task"`
}

// TaskSpec 任务描述，TaskType 决定哪个子字段非空
type TaskSpec struct {
	Type         string            `json:"Type"`                   // "Erase" | "AudioExtract" | ...
	Erase        *EraseSpec        `json:"Erase,omitempty"`        // 当 Type=Erase
	AudioExtract *AudioExtractSpec `json:"AudioExtract,omitempty"` // 当 Type=AudioExtract
}

// EraseSpec 字幕擦除任务参数
type EraseSpec struct {
	Mode          string         `json:"Mode"`                    // "Auto" | "Manual"
	Auto          *EraseAutoSpec `json:"Auto,omitempty"`          // 当 Mode=Auto
	Manual        *EraseManSpec  `json:"Manual,omitempty"`        // 当 Mode=Manual
	EraseOption   *EraseOption   `json:"EraseOption,omitempty"`   // 可选时段过滤
	WithEraseInfo bool           `json:"WithEraseInfo,omitempty"` // 是否返回 PixelRectangle 信息
	NewVid        bool           `json:"NewVid"`                  // 是否为产物生成新 Vid（CCS 建议 true）
}

// EraseAutoSpec Auto 模式参数
type EraseAutoSpec struct {
	Type           string                 `json:"Type"`                     // "Subtitle" | "Text"
	SubtitleFilter map[string]interface{} `json:"SubtitleFilter,omitempty"` // 默认 {}
	Locations      []LocationSpec         `json:"Locations,omitempty"`      // OCR + 矩形限定
}

// EraseManSpec Manual 模式参数（v1 不用，预留）
type EraseManSpec struct {
	Locations []LocationSpec `json:"Locations"` // 强制擦除矩形
}

// EraseOption 时段过滤等附加参数（v1 不用，预留）
type EraseOption struct {
	ClipFilter *ClipFilterSpec `json:"ClipFilter,omitempty"`
}

// ClipFilterSpec 时段过滤
type ClipFilterSpec struct {
	Mode  string     `json:"Mode"` // "Selected" | "Skip"
	Clips []ClipSpec `json:"Clips"`
}

// ClipSpec 单个时间段
type ClipSpec struct {
	Start float64 `json:"Start"`
	End   float64 `json:"End"`
}

// LocationSpec 矩形位置
type LocationSpec struct {
	RatioLocation RatioRect `json:"RatioLocation"`
}

// RatioRect 归一化矩形坐标 [0, 1]
type RatioRect struct {
	TopLeftX     float64 `json:"TopLeftX"`
	TopLeftY     float64 `json:"TopLeftY"`
	BottomRightX float64 `json:"BottomRightX"`
	BottomRightY float64 `json:"BottomRightY"`
}

// AudioExtractSpec 人声背景音分离任务参数
type AudioExtractSpec struct {
	Voice bool `json:"Voice"` // 固定 true 表示提取人声（火山约定）
}

// =====================================================
// StartExecution 响应
// =====================================================

// StartExecutionResponse 提交成功响应
type StartExecutionResponse struct {
	ResponseMetadata map[string]interface{} `json:"ResponseMetadata,omitempty"`
	Result           StartExecutionResult   `json:"Result"`
}

// StartExecutionResult RunId 在 Result.RunId
type StartExecutionResult struct {
	RunId string `json:"RunId"`
}

// =====================================================
// GetExecution 响应
// =====================================================

// GetExecutionResponse 任务查询响应
type GetExecutionResponse struct {
	ResponseMetadata map[string]interface{} `json:"ResponseMetadata,omitempty"`
	Result           GetExecutionResult     `json:"Result"`
}

// GetExecutionResult Status / Output 在 Result 下
type GetExecutionResult struct {
	RunId  string     `json:"RunId"`
	Status string     `json:"Status"` // "Pending" | "Running" | "Success" | "Failed"
	Output OutputSpec `json:"Output"`

	// Status=Failed 时这里可能有错误信息
	Error *OutputError `json:"Error,omitempty"`
}

// OutputSpec 产物
type OutputSpec struct {
	Type string         `json:"Type"`
	Task OutputTaskSpec `json:"Task"`
}

// OutputTaskSpec 任务产物（按 TaskType 分支）
type OutputTaskSpec struct {
	Type         string              `json:"Type"`
	Erase        *EraseOutput        `json:"Erase,omitempty"`
	AudioExtract *AudioExtractOutput `json:"AudioExtract,omitempty"`
}

// EraseOutput Erase 任务产物
type EraseOutput struct {
	Duration float64    `json:"Duration"` // 产物视频时长（秒）
	File     FileObject `json:"File"`
	Info     *EraseInfo `json:"Info,omitempty"` // 仅 WithEraseInfo=true 时返回
}

// EraseInfo 擦除区域信息（v1 不消费，预留）
type EraseInfo struct {
	Width  int            `json:"Width"`
	Height int            `json:"Height"`
	Areas  []EraseInfoSeg `json:"Areas"`
}

// EraseInfoSeg 一段时间内的擦除矩形
type EraseInfoSeg struct {
	Start          float64       `json:"Start"`
	End            float64       `json:"End"`
	PixelRectangle []PixelRect   `json:"PixelRectangle"`
}

// PixelRect 像素矩形坐标
type PixelRect struct {
	TopLeftX     int `json:"TopLeftX"`
	TopLeftY     int `json:"TopLeftY"`
	BottomRightX int `json:"BottomRightX"`
	BottomRightY int `json:"BottomRightY"`
}

// AudioExtractOutput AudioExtract 任务产物
type AudioExtractOutput struct {
	Duration   float64    `json:"Duration"` // 输入视频时长（秒）
	Voice      FileObject `json:"Voice"`
	Background FileObject `json:"Background"`
}

// FileObject 通用产物文件对象
type FileObject struct {
	Size     string `json:"Size"`           // 字节数（字符串形式）
	FileName string `json:"FileName"`       // 加速域名拼接路径
	Vid      string `json:"Vid,omitempty"`  // 仅 Erase + NewVid=true 时有
}

// OutputError Status=Failed 时的错误信息
type OutputError struct {
	Code    string `json:"Code,omitempty"`
	Message string `json:"Message,omitempty"`
}
