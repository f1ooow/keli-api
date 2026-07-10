package ali

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func testRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

func TestConvertToAliRequestWan27I2VBuildsMediaFromImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:    "wan2.7-i2v",
		Prompt:   "animate the first frame",
		Image:    "https://example.com/first.png",
		Size:     "720p",
		Duration: 10,
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "wan2.7-i2v", aliReq.Model)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 10, aliReq.Parameters.Duration)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VBuildsFirstAndLastFrameFromImages(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "interpolate between frames",
		Images: []string{
			"https://example.com/first.png",
			"https://example.com/last.png",
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VPrefersImageBeforeImagesAndInputReference(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "use the direct image",
		Image:          " https://example.com/direct.png ",
		Images:         []string{"https://example.com/images-first.png", " https://example.com/images-last.png "},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/direct.png"},
		{Type: "last_frame", URL: "https://example.com/images-last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VFallsBackToFirstNonEmptyImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "skip blank images",
		Image:  " ",
		Images: []string{
			" ",
			" https://example.com/first.png ",
			" https://example.com/last.png ",
		},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VKeepsExplicitMetadataMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "continue the clip",
		Image:          "https://example.com/direct.png",
		Images:         []string{"https://example.com/images-first.png", "https://example.com/images-last.png"},
		InputReference: "https://example.com/input-reference.png",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"media": []interface{}{
					map[string]interface{}{
						"type": "first_clip",
						"url":  "https://example.com/input.mp4",
					},
				},
			},
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_clip", URL: "https://example.com/input.mp4"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VRequiresMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "animate without a frame",
	}

	_, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "requires image"))
}

func TestConvertToAliRequestWan25I2VKeepsLegacyImgURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate the first frame",
		Image:  "https://example.com/first.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "https://example.com/first.png", aliReq.Input.ImgURL)
	require.Empty(t, aliReq.Input.Media)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"img_url"`)
	require.NotContains(t, string(body), `"media"`)
}

// HappyHorse 是本 fork 自己的家族（不属于上游 Wan 系列），复用 AliVideoMedia
// 做 metadata.media 透传；覆盖这条以确认合并时的字段改名没有破坏我方逻辑。
func TestConvertToAliRequestHappyHorseMediaPassthroughFromMetadata(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "happyhorse-1.0-i2v",
		Prompt: "happyhorse image to video",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"media": []interface{}{
					map[string]interface{}{"type": "first_frame", "url": "https://example.com/frame.png"},
					map[string]interface{}{"type": "reference_image", "url": "https://example.com/ref.png"},
				},
			},
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/frame.png"},
		{Type: "reference_image", URL: "https://example.com/ref.png"},
	}, aliReq.Input.Media)
	// happyhorse 系列不经过 size 校验、默认 1080P 分辨率
	require.Equal(t, "1080P", aliReq.Parameters.Resolution)
}

// 回归锚：DashScope 对"刚提交、尚不可查"的任务会返回 UNKNOWN，曾与 FAILED/CANCELED
// 同等立即判死（FAILURE + 退款 + 永久移出刷新集合），误杀新任务。现在 UNKNOWN 保持
// 非终态，由轮询侧按任务年龄宽限或有界判死（见 service/task_polling.go）。
func TestParseTaskResultUnknownStaysNonTerminal(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"UNKNOWN"}}`))

	require.NoError(t, err)
	require.Equal(t, model.TaskStatusUnknown, taskInfo.Status)
	require.Empty(t, taskInfo.Reason)
}

func TestParseTaskResultUnknownKeepsUpstreamMessage(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{"request_id":"req-1","message":"task not found in scheduler","output":{"task_id":"task-1","task_status":"UNKNOWN"}}`))

	require.NoError(t, err)
	require.Equal(t, model.TaskStatusUnknown, taskInfo.Status)
	require.Equal(t, "task not found in scheduler", taskInfo.Reason)
}

func TestParseTaskResultFailedAndCanceledStayTerminal(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, status := range []string{"FAILED", "CANCELED"} {
		body := fmt.Sprintf(`{"request_id":"req-1","message":"boom","output":{"task_id":"task-1","task_status":"%s"}}`, status)

		taskInfo, err := adaptor.ParseTaskResult([]byte(body))

		require.NoError(t, err)
		require.Equal(t, model.TaskStatusFailure, taskInfo.Status, "status %s", status)
		require.Equal(t, "boom", taskInfo.Reason, "status %s", status)
	}
}

func TestParseTaskResultSucceededAndProgressStatuses(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"SUCCEEDED","video_url":"https://example.com/out.mp4"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, taskInfo.Status)
	require.Equal(t, "https://example.com/out.mp4", taskInfo.Url)

	taskInfo, err = adaptor.ParseTaskResult([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"PENDING"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusQueued, taskInfo.Status)

	taskInfo, err = adaptor.ParseTaskResult([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"RUNNING"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusInProgress, taskInfo.Status)
}

func TestConvertToAliRequestAlwaysSerializesWatermarkFalse(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, model := range []string{"happyhorse-1.0-r2v", "wan2.7-i2v"} {
		req := relaycommon.TaskSubmitReq{
			Model:  model,
			Prompt: "no watermark expected",
			Image:  "https://example.com/first.png",
		}

		aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

		require.NoError(t, err)
		require.False(t, aliReq.Parameters.Watermark)

		body, err := common.Marshal(aliReq)
		require.NoError(t, err)
		// 回归锚：watermark=false 曾被 omitempty 吞掉，DashScope 按默认 true 打水印
		require.Contains(t, string(body), `"watermark":false`, "model %s", model)
	}
}
