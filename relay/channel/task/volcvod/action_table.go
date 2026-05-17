package volcvod

// ActionConfig 一个火山 VOD 媒体处理能力的完整配置
//
// 火山 VOD StartExecution 接口对所有媒体处理任务统一入口；TaskType 决定 body 内 Operation.Task.Type，
// TaskKey 决定具体配置嵌套字段名（与 TaskType 几乎一致，但保留分离以备未来差异）。
type ActionConfig struct {
	TaskType    string  // "Erase" | "AudioExtract" | ...
	TaskKey     string  // Operation.Task.<TaskKey> 嵌套字段名
	Mode        string  // "async"（VOD 媒体处理目前全异步）
	BillingMode string  // "per-output-minute" | "per-input-minute"
	Pricing     float64 // 元/分钟（信息性，实际计费走 newapi ModelPrice）
}

// ActionTable model 名 → ActionConfig 映射
// 添加新 VOD 任务能力时只需在这里加一行（同一 endpoint，无需改 constants.go）
var ActionTable = map[string]ActionConfig{
	// 字幕擦除 - 智能模式（Auto + Subtitle）：OCR 识别字幕并擦除，无 Locations 限定
	"volc-vod-erase-subtitle-auto": {
		TaskType:    "Erase",
		TaskKey:     "Erase",
		Mode:        "async",
		BillingMode: "per-output-minute",
		Pricing:     4.0,
	},
	// 字幕擦除 - 框选模式（Auto + Subtitle + Locations）：OCR 识别 + 用户矩形限定
	"volc-vod-erase-subtitle-locations": {
		TaskType:    "Erase",
		TaskKey:     "Erase",
		Mode:        "async",
		BillingMode: "per-output-minute",
		Pricing:     4.0,
	},
	// 人声背景音分离（AudioExtract）：一次调用返回 Voice + Background 两个 AAC
	"volc-vod-audio-extract": {
		TaskType:    "AudioExtract",
		TaskKey:     "AudioExtract",
		Mode:        "async",
		BillingMode: "per-input-minute",
		Pricing:     0.07,
	},
}

// LookupAction 根据 model 名查 ActionConfig
func LookupAction(modelName string) (ActionConfig, bool) {
	cfg, ok := ActionTable[modelName]
	return cfg, ok
}

// ModelList 返回所有支持的火山 VOD 模型名（用于 newapi 后台 channel 选模型 UI / GetModelList）
func ModelList() []string {
	out := make([]string, 0, len(ActionTable))
	for k := range ActionTable {
		out = append(out, k)
	}
	return out
}
