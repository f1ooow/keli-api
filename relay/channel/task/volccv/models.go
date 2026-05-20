package volccv

// =====================================================
// CVProcess 请求 body (lens_lqir)
// =====================================================

// CVProcessRequest 提交火山 CV 处理任务的统一 body schema
//
// req_key 区分具体能力（lens_lqir / lens_nnsr2_pic_common / ...）。
// lens_lqir 字段如下：
//
//	binary_data_base64    string[]  二选一(优先)：base64 编码图片
//	image_urls            string[]  二选一：图片 URL
//	resolution_boundary   string    "144p" / "240p" / "360p" / "480p" / "540p" / "720p" / "1080p" / "2k"，默认 "720p"
//	enable_hdr            bool      HDR
//	enable_wb             bool      白平衡
//	result_format         int       0=png, 1=jpeg
//	jpg_quality           int       jpeg 质量 [0,100]，默认 95
//	hdr_strength          float     (0,1] 默认 1.0
//	return_url            bool      是否返回 URL (24h 有效)
//
// 本任务一键无参收敛：boundary=2k, hdr=false, wb=false, format=PNG, return_url=true
type CVProcessRequest struct {
	ReqKey             string   `json:"req_key"`
	BinaryDataBase64   []string `json:"binary_data_base64,omitempty"`
	ImageUrls          []string `json:"image_urls,omitempty"`
	ResolutionBoundary string   `json:"resolution_boundary,omitempty"`
	EnableHDR          bool     `json:"enable_hdr,omitempty"`
	EnableWB           bool     `json:"enable_wb,omitempty"`
	ResultFormat       int      `json:"result_format"`
	JpgQuality         int      `json:"jpg_quality,omitempty"`
	HDRStrength        float64  `json:"hdr_strength,omitempty"`
	ReturnUrl          bool     `json:"return_url"`
}

// =====================================================
// CVProcess 响应
// =====================================================

// CVProcessResponse 火山 CV 处理任务响应
//
// 顶层 code/status 含义：
//
//	10000  成功
//	50411  输入图片前审核未通过
//	50511  输出图片后审核未通过
//	50412  输入文本前审核未通过
//	50512  输出文本后审核未通过
//
// data.algorithm_base_resp 是算法内部状态。
// data.image_urls 与 data.binary_data_base64 二选一，由 return_url 控制。
type CVProcessResponse struct {
	Code      int                `json:"code"`
	Status    int                `json:"status"`
	Message   string             `json:"message"`
	RequestId string             `json:"request_id"`
	Data      *CVProcessData     `json:"data,omitempty"`
	TimeElapsed string           `json:"time_elapsed,omitempty"`
}

// CVProcessData CVProcess 响应的 data 字段
type CVProcessData struct {
	AlgorithmBaseResp *AlgorithmBaseResp `json:"algorithm_base_resp,omitempty"`
	BinaryDataBase64  []string           `json:"binary_data_base64,omitempty"`
	ImageUrls         []string           `json:"image_urls,omitempty"`
}

// AlgorithmBaseResp 算法基础响应（算法层错误码不同于顶层）
type AlgorithmBaseResp struct {
	StatusCode    int    `json:"status_code"`
	StatusMessage string `json:"status_message"`
}

// =====================================================
// 包装成 OpenAI-images-edit 风格的响应
// =====================================================

// OpenAIImagesEditResponse OpenAI /v1/images/edits 风格的响应包装
//
// 之所以选这个形状：CCS server 端按 OpenAI-images-edit 风格调用，前端不感知上游差异。
// width / height 是火山没有原生返回的，我们从 preprocess 阶段记录（或返回 url 后下载探测）。
type OpenAIImagesEditResponse struct {
	Created int64                       `json:"created"`
	Data    []OpenAIImagesEditDataItem `json:"data"`
}

// OpenAIImagesEditDataItem OpenAI /v1/images/edits 响应的 data 数组元素
type OpenAIImagesEditDataItem struct {
	URL    string `json:"url,omitempty"`
	B64    string `json:"b64_json,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}
