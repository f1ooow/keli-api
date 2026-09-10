package hailuo

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskAdaptorEndpointURLs(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		baseURL    string
		override   *dto.TaskEndpointOverride
		taskID     string
		wantSubmit string
		wantFetch  string
	}{
		{
			name:       "legacy defaults stay byte for byte compatible",
			model:      "MiniMax-Hailuo-2.3",
			baseURL:    "https://api.minimaxi.com",
			taskID:     "legacy-id",
			wantSubmit: "https://api.minimaxi.com/v1/video_generation",
			wantFetch:  "https://api.minimaxi.com/v1/query/video_generation?task_id=legacy-id",
		},
		{
			name:       "H3 defaults preserve metaso base path",
			model:      ModelMiniMaxH3,
			baseURL:    "https://metaso.cn/api/minimax",
			taskID:     "task/with space",
			wantSubmit: "https://metaso.cn/api/minimax/v2/video_generation",
			wantFetch:  "https://metaso.cn/api/minimax/v2/query/video_generation/task%2Fwith%20space",
		},
		{
			name:    "configured H3 endpoints",
			model:   ModelMiniMaxH3,
			baseURL: "https://relay.example/vendor",
			override: &dto.TaskEndpointOverride{
				SubmitPath: "/custom/create",
				FetchPath:  "/custom/tasks/{task_id}?source=h3",
			},
			taskID:     "123",
			wantSubmit: "https://relay.example/vendor/custom/create",
			wantFetch:  "https://relay.example/vendor/custom/tasks/123?source=h3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl: tt.baseURL,
				ChannelOtherSettings: dto.ChannelOtherSettings{
					TaskEndpointOverride: tt.override,
				},
				UpstreamModelName: tt.model,
			}})
			submit, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				UpstreamModelName: tt.model,
			}})
			require.NoError(t, err)
			assert.Equal(t, tt.wantSubmit, submit)
			fetch, err := adaptor.buildFetchURL(tt.baseURL, tt.taskID, tt.model)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFetch, fetch)
		})
	}
}

func TestBuildH3Request(t *testing.T) {
	watermark := false
	tests := []struct {
		name    string
		req     relaycommon.TaskSubmitReq
		want    *H3VideoRequest
		wantErr string
	}{
		{
			name: "text to video",
			req: relaycommon.TaskSubmitReq{
				Prompt: "space opera", Duration: 5, Size: "2K",
				Metadata: map[string]any{"ratio": "16:9", "aigc_watermark": false},
			},
			want: &H3VideoRequest{
				Model:      ModelMiniMaxH3,
				Content:    []H3ContentItem{{Type: "text", Text: "space opera"}},
				Resolution: Resolution2K, Duration: 5, Ratio: "16:9", AigcWatermark: &watermark,
			},
		},
		{
			name: "first frame forces adaptive",
			req: relaycommon.TaskSubmitReq{
				Prompt: "ramen", Duration: 4, Size: "768P", Mode: "i2v",
				Metadata: map[string]any{"ratio": "16:9", "first_frame_image": "mm_file://image"},
			},
			want: &H3VideoRequest{
				Model: ModelMiniMaxH3,
				Content: []H3ContentItem{
					{Type: "text", Text: "ramen"},
					{Type: "image_url", ImageURL: &H3MediaURL{URL: "mm_file://image"}, Role: "first_frame"},
				},
				Resolution: Resolution768P, Duration: 4, Ratio: "adaptive",
			},
		},
		{
			name: "last frame only forces adaptive",
			req: relaycommon.TaskSubmitReq{
				Prompt: "ending", Duration: 6, Size: "768P", Mode: "i2v",
				Metadata: map[string]any{"last_frame_image": "mm_file://last"},
			},
			want: &H3VideoRequest{
				Model: ModelMiniMaxH3,
				Content: []H3ContentItem{
					{Type: "text", Text: "ending"},
					{Type: "image_url", ImageURL: &H3MediaURL{URL: "mm_file://last"}, Role: "last_frame"},
				},
				Resolution: Resolution768P, Duration: 6, Ratio: "adaptive",
			},
		},
		{
			name: "first and last frames preserve order",
			req: relaycommon.TaskSubmitReq{
				Prompt: "transition", Duration: 7, Size: "2K", Mode: "i2v",
				Metadata: map[string]any{
					"first_frame_image": "mm_file://first",
					"last_frame_image":  "mm_file://last",
				},
			},
			want: &H3VideoRequest{
				Model: ModelMiniMaxH3,
				Content: []H3ContentItem{
					{Type: "text", Text: "transition"},
					{Type: "image_url", ImageURL: &H3MediaURL{URL: "mm_file://first"}, Role: "first_frame"},
					{Type: "image_url", ImageURL: &H3MediaURL{URL: "mm_file://last"}, Role: "last_frame"},
				},
				Resolution: Resolution2K, Duration: 7, Ratio: "adaptive",
			},
		},
		{
			name: "multimodal references",
			req: relaycommon.TaskSubmitReq{
				Prompt: "speak", Duration: 5, Size: "2K", Mode: "all_ref",
				Metadata: map[string]any{
					"reference_images": []string{"mm_file://image"},
					"reference_videos": []string{"mm_file://video"},
					"reference_audios": []string{"mm_file://audio"},
				},
			},
			want: &H3VideoRequest{
				Model: ModelMiniMaxH3,
				Content: []H3ContentItem{
					{Type: "text", Text: "speak"},
					{Type: "image_url", ImageURL: &H3MediaURL{URL: "mm_file://image"}, Role: "reference_image"},
					{Type: "video_url", VideoURL: &H3MediaURL{URL: "mm_file://video"}, Role: "reference_video"},
					{Type: "audio_url", AudioURL: &H3MediaURL{URL: "mm_file://audio"}, Role: "reference_audio"},
				},
				Resolution: Resolution2K, Duration: 5, Ratio: "adaptive",
			},
		},
		{
			name: "reject mixed families",
			req: relaycommon.TaskSubmitReq{
				Prompt: "bad", Duration: 5, Size: "2K",
				Metadata: map[string]any{
					"first_frame_image": "mm_file://first",
					"reference_videos":  []string{"mm_file://video"},
				},
			},
			wantErr: "cannot be mixed",
		},
		{
			name:    "text requires explicit ratio",
			req:     relaycommon.TaskSubmitReq{Prompt: "missing ratio", Duration: 5, Size: "768P"},
			wantErr: "non-adaptive ratio",
		},
		{
			name: "reject too many reference videos",
			req: relaycommon.TaskSubmitReq{
				Prompt: "too many", Duration: 5, Size: "768P", Mode: "all_ref",
				Metadata: map[string]any{"reference_videos": []string{"v1", "v2", "v3", "v4"}},
			},
			wantErr: "at most 3 reference videos",
		},
		{
			name:    "reject invalid duration",
			req:     relaycommon.TaskSubmitReq{Prompt: "bad duration", Duration: 16, Size: "768P", Metadata: map[string]any{"ratio": "16:9"}},
			wantErr: "between 4 and 15",
		},
		{
			name:    "reject invalid resolution",
			req:     relaycommon.TaskSubmitReq{Prompt: "bad resolution", Duration: 5, Size: "1080P", Metadata: map[string]any{"ratio": "16:9"}},
			wantErr: "only supports resolution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildH3Request(&tt.req, ModelMiniMaxH3)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildH3RequestAcceptsStringMetadataAndIgnoresModelOverride(t *testing.T) {
	var req relaycommon.TaskSubmitReq
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"MiniMax-H3",
		"prompt":"hello",
		"duration":"5",
		"size":"2K",
		"metadata":"{\"ratio\":\"16:9\",\"model\":\"attacker-model\"}"
	}`), &req))

	got, err := buildH3Request(&req, ModelMiniMaxH3)
	require.NoError(t, err)
	assert.Equal(t, ModelMiniMaxH3, got.Model)
	assert.Equal(t, 5, got.Duration)
	assert.Equal(t, "16:9", got.Ratio)
}

func TestLegacyMetadataCannotOverrideMappedModel(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt:   "legacy",
		Duration: 6,
		Metadata: map[string]any{"model": "attacker-model"},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MiniMax-Hailuo-2.3"}}

	payload, err := (&TaskAdaptor{}).convertToLegacyRequestPayload(&req, info)
	require.NoError(t, err)
	assert.Equal(t, "MiniMax-Hailuo-2.3", payload.Model)
}

func TestDoH3ResponseAcceptsKnownTaskIDEnvelopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"task_id":"424010985738629"}`,
		`{"result":{"task_id":424010985738629}}`,
	} {
		t.Run(body, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			adaptor := &TaskAdaptor{}
			taskID, data, taskErr := adaptor.doH3Response(ctx, http.StatusOK, []byte(body), &relaycommon.RelayInfo{
				OriginModelName: ModelMiniMaxH3,
				TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
			})
			require.Nil(t, taskErr)
			assert.Equal(t, "424010985738629", taskID)
			assert.Equal(t, body, string(data))
			assert.Contains(t, recorder.Body.String(), "task_public")
		})
	}
}

func TestDoH3ErrorResponsePreservesProviderFields(t *testing.T) {
	adaptor := &TaskAdaptor{}
	resp := &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Body: io.NopCloser(bytes.NewBufferString(`{
			"type":"error",
			"error":{"type":"invalid_request_error","message":"unsupported ratio","http_code":"422"},
			"request_id":"req_h3_123"
		}`)),
	}
	taskErr := adaptor.DoErrorResponse(nil, resp, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		UpstreamModelName: ModelMiniMaxH3,
	}})
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_request_error", taskErr.Code)
	assert.Equal(t, "unsupported ratio", taskErr.Message)
	assert.Equal(t, http.StatusUnprocessableEntity, taskErr.StatusCode)
	assert.Equal(t, map[string]string{"request_id": "req_h3_123"}, taskErr.Data)
}

func TestDoH3ErrorResponseDoesNotExposeMalformedBody(t *testing.T) {
	adaptor := &TaskAdaptor{}
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(bytes.NewBufferString(`provider secret-shaped junk`)),
	}
	taskErr := adaptor.DoErrorResponse(nil, resp, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		UpstreamModelName: ModelMiniMaxH3,
	}})
	require.NotNil(t, taskErr)
	assert.Equal(t, "minimax_h3_error", taskErr.Code)
	assert.Equal(t, "MiniMax-H3 upstream returned HTTP 502", taskErr.Message)
	assert.NotContains(t, taskErr.Message, "secret-shaped")
}

func TestParseH3TaskResult(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus string
		wantURL    string
		wantReason string
	}{
		{name: "queued", body: `{"task":{"id":"1","status":"queued"}}`, wantStatus: "IN_PROGRESS"},
		{name: "running result wrapper", body: `{"result":{"task":{"id":"1","status":"running"}}}`, wantStatus: "IN_PROGRESS"},
		{name: "success", body: `{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/video.mp4"}}}`, wantStatus: "SUCCESS", wantURL: "https://cdn.example/video.mp4"},
		{name: "failed", body: `{"task":{"id":"1","status":"failed","error":{"type":"bad_request_error","message":"blocked"}}}`, wantStatus: "FAILURE", wantReason: "blocked"},
		{name: "cancelled", body: `{"task":{"id":"1","status":"cancelled"}}`, wantStatus: "FAILURE", wantReason: "task failed"},
		{name: "top-level query error", body: `{"type":"error","error":{"type":"not_found_error","message":"task not found"},"request_id":"req-query"}`, wantStatus: "FAILURE", wantReason: "task not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, recognized, err := parseH3TaskResult([]byte(tt.body))
			require.NoError(t, err)
			require.True(t, recognized)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantStatus, string(result.Status))
			assert.Equal(t, tt.wantURL, result.Url)
			assert.Equal(t, tt.wantReason, result.Reason)
		})
	}
}

func TestH3EstimateBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for duration := 4; duration <= 15; duration++ {
		for _, tc := range []struct {
			resolution string
			wantRatio  float64
		}{
			{resolution: Resolution768P, wantRatio: 1},
			{resolution: Resolution2K, wantRatio: H3HighResolutionRatio},
		} {
			t.Run(tc.resolution+"-"+strconv.Itoa(duration), func(t *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Set("task_request", relaycommon.TaskSubmitReq{
					Prompt: "billing", Duration: duration, Size: tc.resolution,
				})
				adaptor := &TaskAdaptor{}
				ratios := adaptor.EstimateBilling(ctx, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: ModelMiniMaxH3,
				}})
				require.NotNil(t, ratios)
				assert.Equal(t, float64(duration), ratios["hailuo-h3-duration"])
				assert.InDelta(t, tc.wantRatio, ratios["hailuo-h3-resolution"], 1e-12)
				assert.InDelta(t, map[bool]float64{true: 0.25, false: 0.2}[tc.resolution == Resolution2K]*float64(duration), 0.2*ratios["hailuo-h3-duration"]*ratios["hailuo-h3-resolution"], 1e-12)
			})
		}
	}
}

func TestBuildRequestBodySerializesH3WithoutLegacyFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "hello", Duration: 5, Size: "2K",
		Metadata: map[string]any{"ratio": "16:9"},
	})
	adaptor := &TaskAdaptor{}
	body, err := adaptor.BuildRequestBody(ctx, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		UpstreamModelName: ModelMiniMaxH3,
	}})
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.True(t, bytes.Contains(data, []byte(`"content"`)))
	assert.False(t, bytes.Contains(data, []byte(`"prompt"`)))
	assert.False(t, bytes.Contains(data, []byte(`"first_frame_image"`)))
	assert.False(t, bytes.Contains(data, []byte(`"mode"`)))
}
