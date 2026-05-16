package viapi

// ChannelName 渠道名称（用于 newapi 后台显示）
const ChannelName = "aliviapi"

// ProductRoute 单个 viapi 子产品的访问元信息
type ProductRoute struct {
	Endpoint string // 如 "videoenhan.cn-shanghai.aliyuncs.com"
	Version  string // 如 "2020-03-20"
}

// ProductRoutes 产品 → 路由元信息映射
// 添加新 viapi 能力时，只需要在这里加新条目（如果是新产品类目）+ 在 ActionTable 加 model 条目
var ProductRoutes = map[string]ProductRoute{
	"videoenhan": {Endpoint: "videoenhan.cn-shanghai.aliyuncs.com", Version: "2020-03-20"},
	"imageseg":   {Endpoint: "imageseg.cn-shanghai.aliyuncs.com", Version: "2019-12-30"},
}

// GetAsyncJobResultAction 异步任务统一查询 Action 名（三个产品都用同一个名字，仅 host/version 不同）
const GetAsyncJobResultAction = "GetAsyncJobResult"
