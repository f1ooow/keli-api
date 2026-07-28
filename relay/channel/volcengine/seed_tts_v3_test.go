package volcengine

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertSeedTTSV3AudioRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "seed-tts-2.0-standard",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "seed-tts-2.0-standard",
		},
	}
	request := dto.AudioRequest{
		Model:          "seed-tts-2.0-standard",
		Input:          "欢迎试听。",
		Voice:          "zh_female_yingyujiaoxue_uranus_bigtts",
		ResponseFormat: "mp3",
		Metadata:       []byte(`{"subtitle_enable":true,"subtitle_type":"sentence"}`),
	}

	body, err := (&Adaptor{}).ConvertAudioRequest(context, info, request)
	require.NoError(t, err)
	require.NotNil(t, body)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)

	upstream := VolcengineSeedTTSV3Request{}
	require.NoError(t, common.Unmarshal(payload, &upstream))
	assert.Equal(t, "newapi-relay-user", upstream.User.UID)
	assert.Equal(t, "欢迎试听。", upstream.ReqParams.Text)
	assert.Equal(t, "seed-tts-2.0-standard", upstream.ReqParams.Model)
	assert.Equal(t, "zh_female_yingyujiaoxue_uranus_bigtts", upstream.ReqParams.Speaker)
	assert.Equal(t, "mp3", upstream.ReqParams.AudioParams.Format)
	assert.Equal(t, 24000, upstream.ReqParams.AudioParams.SampleRate)
	assert.True(t, upstream.ReqParams.AudioParams.EnableSubtitle)
	assert.True(t, context.GetBool(contextKeySeedTTSV3SubtitleEnabled))
	assert.False(t, info.IsStream)
}

func TestSeedTTSV3RequestURLAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "seed-tts-2.0-standard",
			ApiKey:            "appid|access-token",
		},
	}
	adaptor := &Adaptor{}

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://openspeech.bytedance.com/api/v3/tts/unidirectional", requestURL)

	headers := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(context, &headers, info))
	assert.Equal(t, "appid", headers.Get("X-Api-App-Id"))
	assert.Equal(t, "access-token", headers.Get("X-Api-Access-Key"))
	assert.Equal(t, "seed-tts-2.0", headers.Get("X-Api-Resource-Id"))
	assert.NotEmpty(t, headers.Get("X-Api-Request-Id"))
	assert.Empty(t, headers.Get("Authorization"))
}

func TestHandleSeedTTSV3ResponseReturnsSubtitleEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set(contextKeySeedTTSV3SubtitleEnabled, true)
	audioChunk := base64.StdEncoding.EncodeToString([]byte("abc"))
	body := strings.Join([]string{
		`{"code":0,"message":"","data":"` + audioChunk + `"}`,
		`{"code":0,"message":"","sentence":{"text":"欢迎试听。","words":[{"word":"欢","startTime":0.1,"endTime":0.2},{"word":"迎","startTime":0.2,"endTime":0.55}]}}`,
		`{"code":20000000,"message":"OK"}`,
	}, "\n") + "\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Tt-Logid": []string{"trace-volc"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(5)

	usageAny, responseErr := handleSeedTTSV3Response(context, response, info, "mp3")
	require.Nil(t, responseErr)
	usage, ok := usageAny.(*dto.Usage)
	require.True(t, ok)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	envelope := VolcengineSeedTTSSubtitleEnvelope{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.Equal(t, "616263", envelope.Audio)
	assert.Equal(t, "trace-volc", envelope.TraceID)
	require.NotNil(t, envelope.Subtitle)
	assert.Equal(t, []VolcengineSeedTTSSubtitleSentence{{
		Text:      "欢迎试听。",
		StartTime: 100,
		EndTime:   550,
	}}, envelope.Subtitle.Sentences)
}

func TestHandleSeedTTSV3ResponseReturnsBinaryWithoutSubtitles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	audioChunk := base64.StdEncoding.EncodeToString([]byte("mp3"))
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"code":0,"data":"` + audioChunk + `"}` + "\n" +
				`{"code":20000000,"message":"OK"}` + "\n",
		)),
	}
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(3)

	usageAny, responseErr := handleSeedTTSV3Response(context, response, info, "mp3")
	require.Nil(t, responseErr)
	require.NotNil(t, usageAny)
	assert.Equal(t, "audio/mpeg", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "mp3", recorder.Body.String())
}

func TestHandleSeedTTSV3ResponseRejectsIncompleteStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"code":0,"data":"YQ=="}` + "\n")),
	}

	_, responseErr := handleSeedTTSV3Response(context, response, &relaycommon.RelayInfo{}, "mp3")
	require.NotNil(t, responseErr)
	assert.Equal(t, http.StatusBadGateway, responseErr.StatusCode)
}
