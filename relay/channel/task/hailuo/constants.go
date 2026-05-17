package hailuo

const (
	ChannelName = "hailuo-video"
)

var ModelList = []string{
	"MiniMax-Hailuo-2.3",
	"MiniMax-Hailuo-2.3-Fast",
	"MiniMax-Hailuo-02",
	"T2V-01-Director",
	"T2V-01",
	"I2V-01-Director",
	"I2V-01-live",
	"I2V-01",
	"S2V-01",
}

const (
	TextToVideoEndpoint = "/v1/video_generation"
	QueryTaskEndpoint   = "/v1/query/video_generation"
)

const (
	StatusSuccess    = 0
	StatusRateLimit  = 1002
	StatusAuthFailed = 1004
	StatusNoBalance  = 1008
	StatusSensitive  = 1026
	StatusParamError = 2013
	StatusInvalidKey = 2049
)

const (
	TaskStatusPreparing  = "Preparing"
	TaskStatusQueueing   = "Queueing"
	TaskStatusProcessing = "Processing"
	TaskStatusSuccess    = "Success"
	TaskStatusFailed     = "Fail"
)

const (
	Resolution512P  = "512P"
	Resolution720P  = "720P"
	Resolution768P  = "768P"
	Resolution1080P = "1080P"
)

const (
	DefaultDuration   = 6
	DefaultResolution = Resolution720P
)

// HailuoTierRatios 是 Hailuo 视频"分辨率 × 时长"分档倍率表。
// 基准: 每个模型的 ModelPrice 配为 768P 6s 单价，其他档位通过此倍率表换算。
// 维护: MiniMax 官方价表 (https://platform.minimaxi.com/docs/guides/pricing) 更新时同步修改。
//
// key 格式: "<Resolution>-<Duration>"，如 "768P-6"、"1080P-6"、"512P-10"
//
// 期望最终价（ModelPrice × tier ratio × group_ratio × QuotaPerUnit）:
//   MiniMax-Hailuo-2.3-Fast 768P 6s=1.35 / 768P 10s=2.25 / 1080P 6s=2.31
//   MiniMax-Hailuo-2.3      768P 6s=2.00 / 768P 10s=4.00 / 1080P 6s=3.50
//   MiniMax-Hailuo-02       768P 6s=2.00 / 768P 10s=4.00 / 1080P 6s=3.50 / 512P 6s=0.60 / 512P 10s=1.00
var HailuoTierRatios = map[string]map[string]float64{
	"MiniMax-Hailuo-2.3-Fast": {
		"768P-6":  1.0,
		"768P-10": 2.25 / 1.35,
		"1080P-6": 2.31 / 1.35,
	},
	"MiniMax-Hailuo-2.3": {
		"768P-6":  1.0,
		"768P-10": 2.0,
		"1080P-6": 1.75,
	},
	"MiniMax-Hailuo-02": {
		"768P-6":  1.0,
		"768P-10": 2.0,
		"1080P-6": 1.75,
		"512P-6":  0.3,
		"512P-10": 0.5,
	},
}
