package minimax

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type MiniMaxTTSRequest struct {
	Model             string             `json:"model"`
	Text              string             `json:"text"`
	Stream            bool               `json:"stream,omitempty"`
	StreamOptions     *StreamOptions     `json:"stream_options,omitempty"`
	VoiceSetting      VoiceSetting       `json:"voice_setting"`
	PronunciationDict *PronunciationDict `json:"pronunciation_dict,omitempty"`
	AudioSetting      *AudioSetting      `json:"audio_setting,omitempty"`
	TimbreWeights     []TimbreWeight     `json:"timbre_weights,omitempty"`
	LanguageBoost     string             `json:"language_boost,omitempty"`
	VoiceModify       *VoiceModify       `json:"voice_modify,omitempty"`
	SubtitleEnable    *bool              `json:"subtitle_enable,omitempty"`
	SubtitleType      *string            `json:"subtitle_type,omitempty"`
	OutputFormat      string             `json:"output_format,omitempty"`
	AigcWatermark     bool               `json:"aigc_watermark,omitempty"`
}

type StreamOptions struct {
	ExcludeAggregatedAudio bool `json:"exclude_aggregated_audio,omitempty"`
}

type VoiceSetting struct {
	VoiceID           string  `json:"voice_id"`
	Speed             float64 `json:"speed,omitempty"`
	Vol               float64 `json:"vol,omitempty"`
	Pitch             int     `json:"pitch,omitempty"`
	Emotion           string  `json:"emotion,omitempty"`
	TextNormalization bool    `json:"text_normalization,omitempty"`
	LatexRead         bool    `json:"latex_read,omitempty"`
}

type PronunciationDict struct {
	Tone []string `json:"tone,omitempty"`
}

type AudioSetting struct {
	SampleRate int    `json:"sample_rate,omitempty"`
	Bitrate    int    `json:"bitrate,omitempty"`
	Format     string `json:"format,omitempty"`
	Channel    int    `json:"channel,omitempty"`
	ForceCbr   bool   `json:"force_cbr,omitempty"`
}

type TimbreWeight struct {
	VoiceID string `json:"voice_id"`
	Weight  int    `json:"weight"`
}

type VoiceModify struct {
	Pitch        int    `json:"pitch,omitempty"`
	Intensity    int    `json:"intensity,omitempty"`
	Timbre       int    `json:"timbre,omitempty"`
	SoundEffects string `json:"sound_effects,omitempty"`
}

type MiniMaxTTSResponse struct {
	Data      MiniMaxTTSData   `json:"data"`
	ExtraInfo MiniMaxExtraInfo `json:"extra_info"`
	TraceID   string           `json:"trace_id"`
	BaseResp  MiniMaxBaseResp  `json:"base_resp"`
}

type MiniMaxTTSData struct {
	Audio        string `json:"audio"`
	SubtitleFile string `json:"subtitle_file"`
	Status       int    `json:"status"`
}

type MiniMaxExtraInfo struct {
	UsageCharacters int64 `json:"usage_characters"`
}

type MiniMaxBaseResp struct {
	StatusCode int64  `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

const (
	miniMaxTTSSubtitleEnabledContextKey = "minimax_tts_subtitle_enabled"
	maxMiniMaxTTSSubtitleBytes          = 4 << 20
)

type miniMaxTTSSubtitleEnvelope struct {
	Audio    string          `json:"audio,omitempty"`
	AudioURL string          `json:"audio_url,omitempty"`
	Subtitle json.RawMessage `json:"subtitle,omitempty"`
}

type miniMaxTTSSubtitleDownloader func(string, ...string) (*http.Response, error)

func fetchMiniMaxTTSSubtitle(subtitleURL string, download miniMaxTTSSubtitleDownloader) (json.RawMessage, error) {
	response, err := download(subtitleURL, "minimax tts subtitle")
	if err != nil {
		return nil, fmt.Errorf("download subtitle: %w", err)
	}
	if response == nil {
		return nil, errors.New("empty subtitle response")
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected subtitle response status: %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxMiniMaxTTSSubtitleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read subtitle response: %w", err)
	}
	if len(body) > maxMiniMaxTTSSubtitleBytes {
		return nil, fmt.Errorf("subtitle response exceeds %d bytes", maxMiniMaxTTSSubtitleBytes)
	}

	var subtitle json.RawMessage
	if err := common.Unmarshal(body, &subtitle); err != nil {
		return nil, fmt.Errorf("invalid subtitle JSON: %w", err)
	}
	return subtitle, nil
}

func getContentTypeByFormat(format string) string {
	contentTypeMap := map[string]string{
		"mp3":  "audio/mpeg",
		"wav":  "audio/wav",
		"flac": "audio/flac",
		"aac":  "audio/aac",
		"pcm":  "audio/pcm",
	}
	if ct, ok := contentTypeMap[format]; ok {
		return ct
	}
	return "audio/mpeg" // default to mp3
}

func handleTTSResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	return handleTTSResponseWithSubtitleDownloader(c, resp, info, service.DoDownloadRequest)
}

func handleTTSResponseWithSubtitleDownloader(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, downloadSubtitle miniMaxTTSSubtitleDownloader) (usage any, err *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()

	// Parse response
	var minimaxResp MiniMaxTTSResponse
	if unmarshalErr := common.Unmarshal(body, &minimaxResp); unmarshalErr != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax TTS response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
		)
	}

	// Check base_resp status code
	if minimaxResp.BaseResp.StatusCode != 0 {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("minimax TTS error: %d - %s", minimaxResp.BaseResp.StatusCode, minimaxResp.BaseResp.StatusMsg),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
		)
	}

	// Check if we have audio data
	if minimaxResp.Data.Audio == "" {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("no audio data in minimax TTS response"),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
		)
	}

	subtitleEnabled := c.GetBool(miniMaxTTSSubtitleEnabledContextKey)
	if subtitleEnabled {
		envelope := miniMaxTTSSubtitleEnvelope{}
		if strings.HasPrefix(minimaxResp.Data.Audio, "http") {
			envelope.AudioURL = minimaxResp.Data.Audio
		} else {
			envelope.Audio = minimaxResp.Data.Audio
		}

		if minimaxResp.Data.SubtitleFile != "" {
			subtitle, downloadErr := fetchMiniMaxTTSSubtitle(minimaxResp.Data.SubtitleFile, downloadSubtitle)
			if downloadErr != nil {
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("failed to fetch minimax TTS subtitle: %w", downloadErr),
					types.ErrorCodeBadResponseBody,
					http.StatusBadGateway,
				)
			}
			envelope.Subtitle = subtitle
		}

		envelopeBody, marshalErr := common.Marshal(envelope)
		if marshalErr != nil {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to marshal minimax TTS subtitle envelope: %w", marshalErr),
				types.ErrorCodeBadResponseBody,
				http.StatusInternalServerError,
			)
		}
		c.Data(http.StatusOK, "application/json", envelopeBody)
	} else if strings.HasPrefix(minimaxResp.Data.Audio, "http") {
		c.Redirect(http.StatusFound, minimaxResp.Data.Audio)
	} else {
		// Handle hex-encoded audio data
		audioData, decodeErr := hex.DecodeString(minimaxResp.Data.Audio)
		if decodeErr != nil {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to decode hex audio data: %w", decodeErr),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
			)
		}

		// Determine content type - default to mp3
		contentType := "audio/mpeg"

		c.Data(http.StatusOK, contentType, audioData)
	}

	usage = &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      int(minimaxResp.ExtraInfo.UsageCharacters),
	}

	return usage, nil
}

func handleChatCompletionResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("failed to read minimax response"),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()

	// Set response headers
	for key, values := range resp.Header {
		if !service.ShouldCopyUpstreamHeader(c, key, values) {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Data(resp.StatusCode, "application/json", body)
	return nil, nil
}
