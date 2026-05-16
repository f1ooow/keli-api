package viapi

// ActionConfig 一个 viapi 能力的完整配置
type ActionConfig struct {
	Action      string  // x-acs-action 值，如 "EraseVideoSubtitles"
	Product     string  // 产品 code，索引 ProductRoutes，如 "videoenhan"
	Mode        string  // "sync" / "async"
	BillingMode string  // "per-call" / "per-minute"
	Pricing     float64 // 单价 (元；per-minute 模式下为每分钟价；信息性，实际计费走 newapi ModelPrice)
}

// ActionTable model 名 → ActionConfig 映射
// 添加新 viapi 能力时只需在这里加一行（如果是已存在的 product，连 ProductRoutes 都不用动）
var ActionTable = map[string]ActionConfig{
	// 视频超分辨 (videoenhan, 异步)
	"viapi-super-resolve": {
		Action:      "SuperResolveVideo",
		Product:     "videoenhan",
		Mode:        "async",
		BillingMode: "per-minute",
		Pricing:     0.4,
	},
	// 视频字幕擦除 (videoenhan, 异步)
	"viapi-erase-subtitles": {
		Action:      "EraseVideoSubtitles",
		Product:     "videoenhan",
		Mode:        "async",
		BillingMode: "per-minute",
		Pricing:     0.4,
	},
	// 通用图像分割 (imageseg, 同步)
	"viapi-segment-common": {
		Action:      "SegmentCommonImage",
		Product:     "imageseg",
		Mode:        "sync",
		BillingMode: "per-call",
		Pricing:     0.0016,
	},
}

// LookupAction 根据 model 名查 ActionConfig
func LookupAction(modelName string) (ActionConfig, bool) {
	cfg, ok := ActionTable[modelName]
	return cfg, ok
}

// LookupRoute 根据 product 名查 ProductRoute
func LookupRoute(product string) (ProductRoute, bool) {
	r, ok := ProductRoutes[product]
	return r, ok
}

// ModelList 返回所有支持的 viapi 模型名（用于 newapi 后台 channel 选模型 UI）
func ModelList() []string {
	out := make([]string, 0, len(ActionTable))
	for k := range ActionTable {
		out = append(out, k)
	}
	return out
}
