package volcengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const seedTTSV3SuccessCode = 20000000

type VolcengineSeedTTSV3Request struct {
	User      VolcengineSeedTTSV3User      `json:"user"`
	ReqParams VolcengineSeedTTSV3ReqParams `json:"req_params"`
}

type VolcengineSeedTTSV3User struct {
	UID string `json:"uid"`
}

type VolcengineSeedTTSV3ReqParams struct {
	Text        string                         `json:"text"`
	Model       string                         `json:"model"`
	Speaker     string                         `json:"speaker"`
	AudioParams VolcengineSeedTTSV3AudioParams `json:"audio_params"`
}

type VolcengineSeedTTSV3AudioParams struct {
	Format         string `json:"format"`
	SampleRate     int    `json:"sample_rate"`
	EnableSubtitle bool   `json:"enable_subtitle"`
}

type VolcengineSeedTTSV3Metadata struct {
	SubtitleEnable *bool  `json:"subtitle_enable,omitempty"`
	SubtitleType   string `json:"subtitle_type,omitempty"`
}

type VolcengineSeedTTSV3Event struct {
	Code     int                          `json:"code"`
	Message  string                       `json:"message"`
	Data     string                       `json:"data"`
	Sentence *VolcengineSeedTTSV3Sentence `json:"sentence,omitempty"`
}

type VolcengineSeedTTSV3Sentence struct {
	Text  string                    `json:"text"`
	Words []VolcengineSeedTTSV3Word `json:"words"`
}

type VolcengineSeedTTSV3Word struct {
	Word      string  `json:"word"`
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
}

type VolcengineSeedTTSSubtitleEnvelope struct {
	Audio    string                         `json:"audio"`
	Subtitle *VolcengineSeedTTSSubtitleBody `json:"subtitle,omitempty"`
	TraceID  string                         `json:"trace_id,omitempty"`
}

type VolcengineSeedTTSSubtitleBody struct {
	Sentences []VolcengineSeedTTSSubtitleSentence `json:"sentences"`
}

type VolcengineSeedTTSSubtitleSentence struct {
	Text      string `json:"text"`
	StartTime int    `json:"start_time"`
	EndTime   int    `json:"end_time"`
}

func isSeedTTSV3Model(model string) bool {
	return model == "seed-tts-2.0-standard" || model == "doubao-tts-2.0"
}

func convertSeedTTSV3AudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if strings.TrimSpace(request.Input) == "" {
		return nil, errors.New("input is required")
	}
	if strings.TrimSpace(request.Voice) == "" {
		return nil, errors.New("voice is required")
	}

	metadata := VolcengineSeedTTSV3Metadata{}
	if len(request.Metadata) > 0 {
		if err := common.Unmarshal(request.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("error unmarshalling volcengine Seed-TTS metadata: %w", err)
		}
	}
	subtitleEnabled := metadata.SubtitleEnable != nil && *metadata.SubtitleEnable
	encoding := mapEncoding(request.ResponseFormat)
	c.Set(contextKeyResponseFormat, encoding)
	c.Set(contextKeySeedTTSV3SubtitleEnabled, subtitleEnabled)
	info.IsStream = false

	upstreamRequest := VolcengineSeedTTSV3Request{
		User: VolcengineSeedTTSV3User{UID: "newapi-relay-user"},
		ReqParams: VolcengineSeedTTSV3ReqParams{
			Text:    request.Input,
			Model:   "seed-tts-2.0-standard",
			Speaker: request.Voice,
			AudioParams: VolcengineSeedTTSV3AudioParams{
				Format:         encoding,
				SampleRate:     24000,
				EnableSubtitle: subtitleEnabled,
			},
		},
	}
	payload, err := common.Marshal(upstreamRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshalling volcengine Seed-TTS request: %w", err)
	}
	return bytes.NewReader(payload), nil
}

type VolcengineTTSRequest struct {
	App     VolcengineTTSApp     `json:"app"`
	User    VolcengineTTSUser    `json:"user"`
	Audio   VolcengineTTSAudio   `json:"audio"`
	Request VolcengineTTSReqInfo `json:"request"`
}

type VolcengineTTSApp struct {
	AppID   string `json:"appid"`
	Token   string `json:"token"`
	Cluster string `json:"cluster"`
}

type VolcengineTTSUser struct {
	UID string `json:"uid"`
}

type VolcengineTTSAudio struct {
	VoiceType        string  `json:"voice_type"`
	Encoding         string  `json:"encoding"`
	SpeedRatio       float64 `json:"speed_ratio"`
	Rate             int     `json:"rate"`
	Bitrate          int     `json:"bitrate,omitempty"`
	LoudnessRatio    float64 `json:"loudness_ratio,omitempty"`
	EnableEmotion    bool    `json:"enable_emotion,omitempty"`
	Emotion          string  `json:"emotion,omitempty"`
	EmotionScale     float64 `json:"emotion_scale,omitempty"`
	ExplicitLanguage string  `json:"explicit_language,omitempty"`
	ContextLanguage  string  `json:"context_language,omitempty"`
}

type VolcengineTTSReqInfo struct {
	ReqID           string                   `json:"reqid"`
	Text            string                   `json:"text"`
	Operation       string                   `json:"operation"`
	Model           string                   `json:"model,omitempty"`
	TextType        string                   `json:"text_type,omitempty"`
	SilenceDuration float64                  `json:"silence_duration,omitempty"`
	WithTimestamp   interface{}              `json:"with_timestamp,omitempty"`
	ExtraParam      *VolcengineTTSExtraParam `json:"extra_param,omitempty"`
}

type VolcengineTTSExtraParam struct {
	DisableMarkdownFilter      bool                      `json:"disable_markdown_filter,omitempty"`
	EnableLatexTn              bool                      `json:"enable_latex_tn,omitempty"`
	MuteCutThreshold           string                    `json:"mute_cut_threshold,omitempty"`
	MuteCutRemainMs            string                    `json:"mute_cut_remain_ms,omitempty"`
	DisableEmojiFilter         bool                      `json:"disable_emoji_filter,omitempty"`
	UnsupportedCharRatioThresh float64                   `json:"unsupported_char_ratio_thresh,omitempty"`
	AigcWatermark              bool                      `json:"aigc_watermark,omitempty"`
	CacheConfig                *VolcengineTTSCacheConfig `json:"cache_config,omitempty"`
}

type VolcengineTTSCacheConfig struct {
	TextType int  `json:"text_type,omitempty"`
	UseCache bool `json:"use_cache,omitempty"`
}

type VolcengineTTSResponse struct {
	ReqID    string                     `json:"reqid"`
	Code     int                        `json:"code"`
	Message  string                     `json:"message"`
	Sequence int                        `json:"sequence"`
	Data     string                     `json:"data"`
	Addition *VolcengineTTSAdditionInfo `json:"addition,omitempty"`
}

type VolcengineTTSAdditionInfo struct {
	Duration string `json:"duration"`
}

var openAIToVolcengineVoiceMap = map[string]string{
	"alloy":   "zh_male_M392_conversation_wvae_bigtts",
	"echo":    "zh_male_wenhao_mars_bigtts",
	"fable":   "zh_female_tianmei_mars_bigtts",
	"onyx":    "zh_male_zhibei_mars_bigtts",
	"nova":    "zh_female_shuangkuaisisi_mars_bigtts",
	"shimmer": "zh_female_cancan_mars_bigtts",
}

var responseFormatToEncodingMap = map[string]string{
	"mp3":  "mp3",
	"opus": "ogg_opus",
	"aac":  "mp3",
	"flac": "mp3",
	"wav":  "wav",
	"pcm":  "pcm",
}

func parseVolcengineAuth(apiKey string) (appID, token string, err error) {
	parts := strings.Split(apiKey, "|")
	if len(parts) != 2 {
		return "", "", errors.New("invalid api key format, expected: appid|access_token")
	}
	return parts[0], parts[1], nil
}

func mapVoiceType(openAIVoice string) string {
	if voice, ok := openAIToVolcengineVoiceMap[openAIVoice]; ok {
		return voice
	}
	return openAIVoice
}

func mapEncoding(responseFormat string) string {
	if encoding, ok := responseFormatToEncodingMap[responseFormat]; ok {
		return encoding
	}
	return "mp3"
}

func getContentTypeByEncoding(encoding string) string {
	contentTypeMap := map[string]string{
		"mp3":      "audio/mpeg",
		"ogg_opus": "audio/ogg",
		"wav":      "audio/wav",
		"pcm":      "audio/pcm",
	}
	if ct, ok := contentTypeMap[encoding]; ok {
		return ct
	}
	return "application/octet-stream"
}

func handleTTSResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, encoding string) (usage any, err *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("failed to read volcengine response"),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()

	var volcResp VolcengineTTSResponse
	if unmarshalErr := common.Unmarshal(body, &volcResp); unmarshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("failed to parse volcengine response"),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
		)
	}

	if volcResp.Code != 3000 {
		return nil, types.NewErrorWithStatusCode(
			errors.New(volcResp.Message),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
		)
	}

	audioData, decodeErr := base64.StdEncoding.DecodeString(volcResp.Data)
	if decodeErr != nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("failed to decode audio data"),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
		)
	}

	contentType := getContentTypeByEncoding(encoding)
	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, audioData)

	usage = &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      info.GetEstimatePromptTokens(),
	}

	return usage, nil
}

func handleSeedTTSV3Response(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, encoding string) (usage any, err *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("volcengine Seed-TTS returned an empty response"),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	defer resp.Body.Close()

	traceID := resp.Header.Get("X-Tt-Logid")
	if traceID == "" {
		traceID = resp.Header.Get("X-Log-Id")
	}

	var audio bytes.Buffer
	subtitles := make([]VolcengineSeedTTSSubtitleSentence, 0)
	seenSubtitles := make(map[string]struct{})
	completed := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		event := VolcengineSeedTTSV3Event{}
		if unmarshalErr := common.Unmarshal(line, &event); unmarshalErr != nil {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to parse volcengine Seed-TTS event: %w", unmarshalErr),
				types.ErrorCodeBadResponseBody,
				http.StatusBadGateway,
			)
		}
		if event.Code == seedTTSV3SuccessCode {
			completed = true
			continue
		}
		if event.Code != 0 {
			statusCode := http.StatusBadGateway
			if event.Code == 40000701 || event.Code == 40000702 {
				statusCode = http.StatusUnauthorized
			}
			message := strings.TrimSpace(event.Message)
			if message == "" {
				message = "volcengine Seed-TTS request failed"
			}
			if traceID != "" {
				message = fmt.Sprintf("%s (trace id: %s)", message, traceID)
			}
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("volcengine Seed-TTS error %d: %s", event.Code, message),
				types.ErrorCodeBadResponse,
				statusCode,
			)
		}

		if event.Data != "" {
			chunk, decodeErr := base64.StdEncoding.DecodeString(event.Data)
			if decodeErr != nil {
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("failed to decode volcengine Seed-TTS audio chunk: %w", decodeErr),
					types.ErrorCodeBadResponseBody,
					http.StatusBadGateway,
				)
			}
			_, _ = audio.Write(chunk)
		}

		if event.Sentence != nil && len(event.Sentence.Words) > 0 {
			text := strings.TrimSpace(event.Sentence.Text)
			startTime := event.Sentence.Words[0].StartTime
			endTime := event.Sentence.Words[len(event.Sentence.Words)-1].EndTime
			if text != "" && startTime >= 0 && endTime > startTime {
				subtitle := VolcengineSeedTTSSubtitleSentence{
					Text:      text,
					StartTime: int(math.Round(startTime * 1000)),
					EndTime:   int(math.Round(endTime * 1000)),
				}
				key := fmt.Sprintf("%d:%d:%s", subtitle.StartTime, subtitle.EndTime, subtitle.Text)
				if _, exists := seenSubtitles[key]; !exists {
					seenSubtitles[key] = struct{}{}
					subtitles = append(subtitles, subtitle)
				}
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read volcengine Seed-TTS stream: %w", scanErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusBadGateway,
		)
	}
	if !completed || audio.Len() == 0 {
		return nil, types.NewErrorWithStatusCode(
			errors.New("volcengine Seed-TTS response was incomplete"),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}

	if c.GetBool(contextKeySeedTTSV3SubtitleEnabled) {
		envelope := VolcengineSeedTTSSubtitleEnvelope{
			Audio:   hex.EncodeToString(audio.Bytes()),
			TraceID: traceID,
			Subtitle: &VolcengineSeedTTSSubtitleBody{
				Sentences: subtitles,
			},
		}
		body, marshalErr := common.Marshal(envelope)
		if marshalErr != nil {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to marshal volcengine Seed-TTS response: %w", marshalErr),
				types.ErrorCodeBadResponseBody,
				http.StatusInternalServerError,
			)
		}
		c.Data(http.StatusOK, "application/json", body)
	} else {
		contentType := getContentTypeByEncoding(encoding)
		c.Data(http.StatusOK, contentType, audio.Bytes())
	}

	usage = &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      info.GetEstimatePromptTokens(),
	}
	return usage, nil
}

func generateRequestID() string {
	return uuid.New().String()
}

func handleTTSWebSocketResponse(c *gin.Context, requestURL string, volcRequest VolcengineTTSRequest, info *relaycommon.RelayInfo, encoding string) (usage any, err *types.NewAPIError) {
	_, token, parseErr := parseVolcengineAuth(info.ApiKey)
	if parseErr != nil {
		return nil, types.NewErrorWithStatusCode(
			parseErr,
			types.ErrorCodeChannelInvalidKey,
			http.StatusUnauthorized,
		)
	}

	header := http.Header{}
	header.Set("Authorization", fmt.Sprintf("Bearer;%s", token))

	conn, resp, dialErr := websocket.DefaultDialer.DialContext(context.Background(), requestURL, header)
	if dialErr != nil {
		if resp != nil {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to connect to websocket: %w, status: %d", dialErr, resp.StatusCode),
				types.ErrorCodeBadResponseStatusCode,
				http.StatusBadGateway,
			)
		}
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to connect to websocket: %w", dialErr),
			types.ErrorCodeBadResponseStatusCode,
			http.StatusBadGateway,
		)
	}
	defer conn.Close()

	payload, marshalErr := common.Marshal(volcRequest)
	if marshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to marshal request: %w", marshalErr),
			types.ErrorCodeBadRequestBody,
			http.StatusInternalServerError,
		)
	}

	if sendErr := FullClientRequest(conn, payload); sendErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to send request: %w", sendErr),
			types.ErrorCodeBadRequestBody,
			http.StatusInternalServerError,
		)
	}

	contentType := getContentTypeByEncoding(encoding)
	c.Header("Content-Type", contentType)
	c.Header("Transfer-Encoding", "chunked")

	for {
		msg, recvErr := ReceiveMessage(conn)
		if recvErr != nil {
			if websocket.IsCloseError(recvErr, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				break
			}
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to receive message: %w", recvErr),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
			)
		}

		switch msg.MsgType {
		case MsgTypeError:
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("received error from server: code=%d, %s", msg.ErrorCode, string(msg.Payload)),
				types.ErrorCodeBadResponse,
				http.StatusBadRequest,
			)
		case MsgTypeFrontEndResultServer:
			continue
		case MsgTypeAudioOnlyServer:
			if len(msg.Payload) > 0 {
				if _, writeErr := c.Writer.Write(msg.Payload); writeErr != nil {
					return nil, types.NewErrorWithStatusCode(
						fmt.Errorf("failed to write audio data: %w", writeErr),
						types.ErrorCodeBadResponse,
						http.StatusInternalServerError,
					)
				}
				c.Writer.Flush()
			}

			if msg.Sequence < 0 {
				c.Status(http.StatusOK)
				usage = &dto.Usage{
					PromptTokens:     info.GetEstimatePromptTokens(),
					CompletionTokens: 0,
					TotalTokens:      info.GetEstimatePromptTokens(),
				}
				return usage, nil
			}
		default:
			continue
		}
	}

	c.Status(http.StatusOK)
	usage = &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      info.GetEstimatePromptTokens(),
	}
	return usage, nil
}
