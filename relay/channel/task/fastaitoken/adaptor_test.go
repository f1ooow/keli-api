package fastaitoken

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/hailuo"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 画布 seedance driver 的真实请求体：resolution/ratio 在 metadata 里、时长在顶层 seconds（字符串）。
func seedanceRequest() relaycommon.TaskSubmitReq {
	return relaycommon.TaskSubmitReq{
		Model:   "doubao-seedance-2-0-mini-260615",
		Prompt:  "a red paper boat",
		Seconds: "4",
		Metadata: map[string]any{
			"ratio":          "16:9",
			"resolution":     "720p",
			"generate_audio": false,
			"camera_fixed":   false,
			"watermark":      false,
		},
	}
}

// 画布 minimax-h3 driver 的真实请求体：时长在顶层 duration（int）、分辨率在顶层 size 且是大写。
func h3Request() relaycommon.TaskSubmitReq {
	return relaycommon.TaskSubmitReq{
		Model:    "MiniMax-H3",
		Prompt:   "a red paper boat",
		Duration: 4,
		Size:     "768P",
		Metadata: map[string]any{"ratio": "16:9", "aigc_watermark": false},
	}
}

func TestBuildPayloadFlattensSeedanceRequest(t *testing.T) {
	req := seedanceRequest()
	payload, err := buildPayload(&req)
	require.NoError(t, err)

	assert.Equal(t, "doubao-seedance-2-0-mini-260615", payload.Model)
	assert.Equal(t, "a red paper boat", payload.Prompt)
	assert.Equal(t, "text_to_video", payload.Mode)
	// FastAI 上游要求这些任务参数位于顶层。
	assert.Equal(t, "720p", payload.Resolution)
	assert.Equal(t, "16:9", payload.Ratio)
	assert.Equal(t, 4, payload.Duration)
	require.NotNil(t, payload.GenerateAudio)
	assert.False(t, *payload.GenerateAudio)
	require.NotNil(t, payload.Watermark)
	assert.False(t, *payload.Watermark)
}

func TestBuildPayloadFlattensH3RequestAndLowercasesResolution(t *testing.T) {
	req := h3Request()
	payload, err := buildPayload(&req)
	require.NoError(t, err)

	assert.Equal(t, "MiniMax-H3", payload.Model)
	// 实测：上游只接受小写 768p，大写会被忽略并回落默认档。
	assert.Equal(t, "768p", payload.Resolution)
	assert.Equal(t, "16:9", payload.Ratio)
	assert.Equal(t, 4, payload.Duration)
	require.NotNil(t, payload.Watermark)
	assert.False(t, *payload.Watermark)
}

func TestBuildPayloadForwardsSeedanceReferenceMedia(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Model:    "seedance-2.0-fast-time",
		Prompt:   "use the reference materials",
		Duration: 6,
		Mode:     "omni",
		Metadata: map[string]any{
			"ratio":            "21:9",
			"resolution":       "720p",
			"reference_images": []string{"https://example.com/ref.png"},
			"reference_videos": []string{"https://example.com/ref.mp4"},
			"reference_audios": []string{"https://example.com/ref.mp3"},
		},
	}
	payload, err := buildPayload(&req)
	require.NoError(t, err)

	assert.Equal(t, "omni", payload.Mode)
	assert.Equal(t, []string{"https://example.com/ref.png"}, payload.Images)
	assert.Equal(t, []string{"https://example.com/ref.mp4"}, payload.Videos)
	assert.Equal(t, []string{"https://example.com/ref.mp3"}, payload.Audio)
}

func TestBuildPayloadDerivesOmniModeFromVideoReference(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Model:    "seedance-2.0-time",
		Prompt:   "use the reference video",
		Duration: 4,
		Metadata: map[string]any{
			"reference_videos": []string{"https://example.com/ref.mp4"},
		},
	}
	payload, err := buildPayload(&req)
	require.NoError(t, err)
	assert.Equal(t, "omni", payload.Mode)
}

func TestBuildPayloadExtractsSeedanceContentMedia(t *testing.T) {
	req := seedanceRequest()
	req.Metadata["content"] = []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/ref.png"}},
		map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/ref.mp4"}},
	}
	payload, err := buildPayload(&req)
	require.NoError(t, err)
	assert.Equal(t, "omni", payload.Mode)
	assert.Equal(t, []string{"https://example.com/ref.png"}, payload.Images)
	assert.Equal(t, []string{"https://example.com/ref.mp4"}, payload.Videos)
}

func TestSeedanceVideoReferenceKeepsGatewayTokenPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := seedanceRequest()
	req.Metadata["reference_videos"] = []string{"https://example.com/ref.mp4"}
	ctx.Set("task_request", req)
	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2-0-260128",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "seedance-2.0-time"},
	})
	require.NotNil(t, ratios)
	assert.InDelta(t, 28.0/46.0, ratios["video_input"], 1e-12)
}

func TestHasMediaInputCoversBothDriverShapes(t *testing.T) {
	textOnly := seedanceRequest()
	meta, err := parseMetadata(&textOnly)
	require.NoError(t, err)
	assert.False(t, hasMediaInput(&textOnly, meta))

	seedanceFrames := seedanceRequest()
	seedanceFrames.Metadata["content"] = []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/a.png"}, "role": "first_frame"},
	}
	meta, err = parseMetadata(&seedanceFrames)
	require.NoError(t, err)
	assert.True(t, hasMediaInput(&seedanceFrames, meta))

	h3Frames := h3Request()
	h3Frames.Metadata["first_frame_image"] = "https://x/a.png"
	meta, err = parseMetadata(&h3Frames)
	require.NoError(t, err)
	assert.True(t, hasMediaInput(&h3Frames, meta))

	h3Refs := h3Request()
	h3Refs.Metadata["reference_images"] = []any{"https://x/a.png"}
	meta, err = parseMetadata(&h3Refs)
	require.NoError(t, err)
	assert.True(t, hasMediaInput(&h3Refs, meta))
}

func TestParseTaskResultFillsVideoURLOnCompleted(t *testing.T) {
	// fastaitoken 实测成功响应样本（URL 已截短）。
	body := []byte(`{"id":"abc","task_id":"abc","object":"video.generation.task","status":"completed",` +
		`"progress":100,"usage":{"total_tokens":87277,"completion_tokens":87277},` +
		`"video_url":"https://vod.example/v.mp4?auth_key=1","result_url":"https://vod.example/v.mp4?auth_key=1",` +
		`"download_url":"https://vod.example/v.mp4?auth_key=1"}`)

	adaptor := &TaskAdaptor{}
	info, err := adaptor.ParseTaskResult(body)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), model.TaskStatus(info.Status))
	// 画布读的是 result_url，这里不回填就永远拿不到视频。
	assert.Equal(t, "https://vod.example/v.mp4?auth_key=1", info.Url)
	assert.Equal(t, 87277, info.CompletionTokens)
}

func TestParseTaskResultStatusMapping(t *testing.T) {
	adaptor := &TaskAdaptor{}
	cases := []struct {
		body   string
		status model.TaskStatus
	}{
		{`{"status":"queued"}`, model.TaskStatus(model.TaskStatusQueued)},
		{`{"status":"processing","progress":63}`, model.TaskStatusInProgress},
		{`{"status":"succeeded","video_url":"https://vod.example/v.mp4"}`, model.TaskStatusSuccess},
		{`{"status":"failed","error":{"message":"content rejected"}}`, model.TaskStatusFailure},
		{`{"status":"expired"}`, model.TaskStatusFailure},
	}
	for _, tc := range cases {
		info, err := adaptor.ParseTaskResult([]byte(tc.body))
		require.NoError(t, err)
		assert.Equal(t, tc.status, model.TaskStatus(info.Status), tc.body)
	}

	info, err := adaptor.ParseTaskResult([]byte(`{"status":"processing","progress":63}`))
	require.NoError(t, err)
	assert.Equal(t, "63%", info.Progress)
}

func TestBuildRequestURLAndFetchURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl: "https://fastaitoken.com",
	}})
	submit, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{})
	require.NoError(t, err)
	assert.Equal(t, "https://fastaitoken.com/v1/videos/generations", submit)
}

// 同一个模型换到本渠道后用户付的钱必须不变：倍率 key 与数值都对齐原渠道。
func TestEstimateBillingMatchesOriginalChannels(t *testing.T) {
	req := h3Request()
	req.Size = "2K"
	req.Duration = 6
	meta, err := parseMetadata(&req)
	require.NoError(t, err)
	assert.Equal(t, "2k", resolveResolution(&req, meta))
	assert.Equal(t, 6, resolveDuration(&req))
	assert.True(t, isH3Model("MiniMax-H3"))
	assert.False(t, isH3Model("doubao-seedance-2-0-mini-260615"))

	seedance := seedanceRequest()
	assert.Equal(t, 4, resolveDuration(&seedance))
}

func TestH3EstimateBillingUsesFastAI1080pHighResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		size       string
		wantRatio  float64
		wantResult bool
	}{
		{name: "768p", size: "768p", wantRatio: 1, wantResult: true},
		{name: "1080p", size: "1080p", wantRatio: hailuo.H3HighResolutionRatio, wantResult: true},
		{name: "native 2k rejected by FastAI", size: "2k", wantResult: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			req := h3Request()
			req.Size = tc.size
			ctx.Set("task_request", req)
			ratio := (&TaskAdaptor{}).EstimateBilling(ctx, &relaycommon.RelayInfo{
				OriginModelName: "MiniMax-H3",
				ChannelMeta:     &relaycommon.ChannelMeta{},
			})
			if !tc.wantResult {
				assert.Nil(t, ratio)
				return
			}
			require.NotNil(t, ratio)
			assert.Equal(t, tc.wantRatio, ratio["hailuo-h3-resolution"])
			assert.Equal(t, float64(req.Duration), ratio["hailuo-h3-duration"])
		})
	}
}
