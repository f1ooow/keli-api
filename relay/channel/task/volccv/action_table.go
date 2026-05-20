package volccv

// ActionConfig 一个火山 CV 能力的完整配置
//
// 火山 CV 类接口都走 CVProcess Action；ReqKey 决定 body.req_key 字段值，对应具体能力。
type ActionConfig struct {
	ReqKey      string  // body.req_key 值，如 "lens_lqir"
	Mode        string  // "sync"（CV 类接口绝大多数 5-10s 同步返回）
	BillingMode string  // "per-call"（按次计费）
	Pricing     float64 // 元/次（信息性，实际计费走 newapi ModelPrice）
}

// ActionTable model 名 → ActionConfig 映射
//
// 添加新 CV 能力时只需在这里加一行（同一 Endpoint + Action，只换 ReqKey）。
var ActionTable = map[string]ActionConfig{
	// 通用图像增强 / 超分 / 去模糊（lens_lqir）
	// 行为：< boundary 走 ×2 超分；>= boundary 走去模糊保分辨率
	// 输入限制：JPG/JPEG/PNG/BMP, ≤5MB, width∈[50,2128], height∈[50,4046]
	"volc-lens-lqir": {
		ReqKey:      "lens_lqir",
		Mode:        "sync",
		BillingMode: "per-call",
		Pricing:     0.01,
	},
}

// LookupAction 根据 model 名查 ActionConfig
func LookupAction(modelName string) (ActionConfig, bool) {
	cfg, ok := ActionTable[modelName]
	return cfg, ok
}

// LookupActionByReqKey 根据 ReqKey 字符串反查 ActionConfig
// 用于在 RelayInfo.Action 已设置但 model 不可访问时使用
func LookupActionByReqKey(reqKey string) (ActionConfig, bool) {
	for _, cfg := range ActionTable {
		if cfg.ReqKey == reqKey {
			return cfg, true
		}
	}
	return ActionConfig{}, false
}

// ModelList 返回所有支持的火山 CV 模型名（用于 newapi 后台 channel 选模型 UI / GetModelList）
func ModelList() []string {
	out := make([]string, 0, len(ActionTable))
	for k := range ActionTable {
		out = append(out, k)
	}
	return out
}
