package minimax

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertAudioRequestForwardsSubtitleMetadataAndSetsResponseGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := dto.AudioRequest{
		Input:          "测试字幕",
		Voice:          "male-qn-qingse",
		ResponseFormat: "mp3",
		Metadata:       []byte(`{"subtitle_enable":true,"subtitle_type":"sentence"}`),
	}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "speech-2.8-hd",
	}

	body, err := (&Adaptor{}).ConvertAudioRequest(context, info, request)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, common.Unmarshal(payload, &got))
	assert.Equal(t, true, got["subtitle_enable"])
	assert.Equal(t, "sentence", got["subtitle_type"])
	assert.True(t, context.GetBool(miniMaxTTSSubtitleEnabledContextKey))
}

func TestHandleTTSResponseReturnsSubtitleEnvelopeOnlyWhenRequested(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set(miniMaxTTSSubtitleEnabledContextKey, true)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"data":{"audio":"6879","subtitle_file":"https://example.com/subtitle.json","status":2},
			"extra_info":{"usage_characters":4},
			"base_resp":{"status_code":0,"status_msg":"success"}
		}`)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioSpeech,
		StartTime: time.Unix(1700000000, 0),
	}
	downloader := func(url string, reason ...string) (*http.Response, error) {
		assert.Equal(t, "https://example.com/subtitle.json", url)
		assert.Equal(t, []string{"minimax tts subtitle"}, reason)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"sentences":[{"text":"测试","start_time":0,"end_time":800}]}`)),
		}, nil
	}

	usage, apiErr := handleTTSResponseWithSubtitleDownloader(context, response, info, downloader)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	var envelope map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.Equal(t, "6879", envelope["audio"])
	assert.NotContains(t, envelope, "audio_url")
	subtitle, ok := envelope["subtitle"].(map[string]any)
	require.True(t, ok)
	assert.Len(t, subtitle["sentences"], 1)
}

func TestHandleTTSResponseKeepsBinaryContractWithoutSubtitleRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"data":{"audio":"6869","subtitle_file":"https://example.com/subtitle.json","status":2},
			"extra_info":{"usage_characters":2},
			"base_resp":{"status_code":0,"status_msg":"success"}
		}`)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioSpeech,
		StartTime: time.Unix(1700000000, 0),
	}
	downloaderCalled := false

	_, apiErr := handleTTSResponseWithSubtitleDownloader(context, response, info, func(string, ...string) (*http.Response, error) {
		downloaderCalled = true
		return nil, nil
	})
	require.Nil(t, apiErr)
	assert.False(t, downloaderCalled)
	assert.Equal(t, "hi", recorder.Body.String())
	assert.Equal(t, "audio/mpeg", recorder.Header().Get("Content-Type"))
}
