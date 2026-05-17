package volcvod

// ChannelName 渠道名称（用于 newapi 后台显示）
const ChannelName = "volcvod"

// 火山引擎 VOD OpenAPI 公共参数
const (
	Endpoint = "vod.volcengineapi.com"
	Region   = "cn-north-1"
	Service  = "vod"
	Version  = "2025-01-01"
)

// StartExecution / GetExecution 是火山 VOD 媒体处理类任务统一入口
// AudioExtract / Erase / Transcode 等都通过同一对 Action 提交+查询，区别仅在 body 内 Operation.Task.Type
const (
	ActionStartExecution = "StartExecution"
	ActionGetExecution   = "GetExecution"
)
