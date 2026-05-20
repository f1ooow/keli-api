package volccv

// ChannelName 渠道名称（用于 newapi 后台显示）
const ChannelName = "volccv"

// 火山引擎 CV (Computer Vision) OpenAPI 公共参数
//
// 与 volcvod 不同的 service 字符串：vod 是点播媒体处理，cv 是计算机视觉
// （图像增强 / 超分 / 抠图 / 修复 / 字幕擦除 等）。host / region 也不一样。
const (
	Endpoint = "visual.volcengineapi.com"
	Region   = "cn-north-1"
	Service  = "cv"
	Version  = "2022-08-31"
)

// 火山 CV 类能力统一通过 CVProcess Action 提交。
// 通过 body 内的 req_key 字段区分具体能力（lens_lqir / lens_nnsr2_pic_common / ...）。
const (
	ActionCVProcess = "CVProcess"
)
