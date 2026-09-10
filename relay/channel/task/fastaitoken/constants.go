package fastaitoken

const ChannelName = "fastaitoken"

// ModelList 是本渠道类型在 /api/models 里对外声明的上游模型名。
// 2.0/2.0 Fast 优先使用 time 计费版本；Mini/2.5 没有 time 模型，使用更低价的 token 版本。
var ModelList = []string{
	"seedance-2.0-time",
	"seedance-2.0-fast-time",
	"seedance-2.0-mini-token",
	"seedance-2.5-token",
}

const (
	// SubmitEndpoint 与 FetchEndpoint 是 fastaitoken 实测可用的异步视频任务端点。
	// 站点同时提供 /v1/videos（Sora 形态）指向同一 handler，这里固定用带
	// /generations 的一组，与提交路径同族。
	SubmitEndpoint = "/v1/videos/generations"
	FetchEndpoint  = "/v1/videos/generations/%s"
)

// 上游状态词（实测样本：processing / completed；queued 与 failed 按站点文档同族取值兜底）。
const (
	StatusQueued     = "queued"
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusRunning    = "running"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusExpired    = "expired"
	StatusCancelled  = "cancelled"
)
