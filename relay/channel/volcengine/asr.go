// Package volcengine - Doubao Seed ASR ("录音文件识别 2.0") adapter.
//
// Flow:
//
//  1. Client POSTs multipart audio to `/v1/audio/transcriptions` with the
//     volcengine channel selected (model = `doubao-asr-2.0` or similar).
//  2. ConvertAudioRequest reads the `file` part bytes.
//  3. The bytes are uploaded to Volcengine VOD (via vod_uploader.go) using
//     VOD credentials stashed in the channel's OtherInfo map.
//  4. We build a public playback URL `<scheme>://<playback-domain>/<FileName>`
//     and submit it to `https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit`.
//  5. We poll `/query` every 2s until X-Api-Status-Code = 20000000 (success)
//     or a terminal error, with a 5min ceiling.
//  6. utterances → SRT text + segments[]. We return an OpenAI Whisper-compatible
//     JSON `{ text, language, duration, segments }`.
//
// API key format on the channel: "<APP_ID>|<ACCESS_TOKEN>", same as TTS
// (`<APP_ID>|<ACCESS_TOKEN>`). The ACCESS_TOKEN is sent as X-Api-Key; the
// APP_ID is used in `request.user.uid` so Volcengine logs can attribute the
// call.
//
// Tempo / public URL caveat: the audio URL given to Doubao MUST be reachable
// from Volcengine's data plane. Public CDN-fronted VOD playback URLs satisfy
// this (URL-auth signed when the operator has it enabled). Localhost / LAN
// URLs will not work — that's why we upload to VOD instead of streaming
// directly.
//
// TODO: delete the temporary VOD media after ASR completes. Currently the
// uploaded file lingers in the operator's VOD space. The TS volcvod driver
// has this cleanup; we should replicate it here once we have a clear retention
// policy. The cost per minute is tiny (~¥0.0003) so it isn't a launch blocker.
package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	asrSubmitEndpoint = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit"
	asrQueryEndpoint  = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/query"
	asrResourceID     = "volc.seedasr.auc"

	asrPollIntervalSecs = 2
	asrMaxPollSecs      = 300 // 5 minutes
)

// X-Api-Status-Code (header) values per Doubao ASR error code reference.
const (
	asrStatusSuccess    = "20000000"
	asrStatusProcessing = "20000001"
	asrStatusQueued     = "20000002"
	asrStatusSilent     = "20000003"
)

// Module-level seam for tests to swap in a fake HTTP client / VOD uploader.
//
//	asrHTTPClient and asrVodUploader default to real implementations; tests
//	set them to deterministic fakes via the (un-exported) setters.
var (
	asrHTTPClient = &http.Client{Timeout: 20 * time.Second}

	// uploadFn is the indirection seam for the VOD upload step. Tests can
	// override this to avoid actually hitting Volcengine VOD.
	asrUploadFn = UploadMediaToVOD

	// nowFn is the time source. Tests can stub this to drive deterministic
	// polling without sleeping.
	asrNowFn = time.Now

	// sleepFn is the poll-delay seam. Tests can stub it to return immediately.
	asrSleepFn = time.Sleep
)

// asrRequest is the body shape we POST to Doubao ASR submit.
type asrRequest struct {
	User    asrUserInfo    `json:"user"`
	Audio   asrAudioInfo   `json:"audio"`
	Request asrRequestInfo `json:"request"`
}

type asrUserInfo struct {
	UID string `json:"uid"`
}

type asrAudioInfo struct {
	URL      string `json:"url"`
	Format   string `json:"format"`             // mp3 / wav / ...
	Language string `json:"language,omitempty"` // e.g. "zh-CN"
}

type asrRequestInfo struct {
	ModelName      string `json:"model_name"`            // "bigmodel"
	EnableITN      bool   `json:"enable_itn,omitempty"`  // 文字归一化（数字/日期）
	EnablePunc     bool   `json:"enable_punc,omitempty"` // 标点
	ShowUtterances bool   `json:"show_utterances,omitempty"`
}

// asrSubmitResponse is the JSON body of the submit endpoint.
type asrSubmitResponse struct {
	// Some payloads return `task_id`, some only convey it via the request id
	// echoed in headers; tolerate both.
	TaskID  string `json:"task_id,omitempty"`
	Message string `json:"message,omitempty"`
}

// asrQueryResponse is the JSON body of the query endpoint when a result is
// ready. Schema based on the public Doubao录音识别 2.0 docs (Aug 2025 rev).
type asrQueryResponse struct {
	AudioInfo struct {
		Duration float64 `json:"duration,omitempty"`
	} `json:"audio_info,omitempty"`
	Result struct {
		Text       string            `json:"text,omitempty"`
		Utterances []asrUtterance    `json:"utterances,omitempty"`
		Additions  map[string]string `json:"additions,omitempty"`
	} `json:"result,omitempty"`
	Message string `json:"message,omitempty"`
}

type asrUtterance struct {
	Text      string   `json:"text"`
	StartTime int64    `json:"start_time"` // ms
	EndTime   int64    `json:"end_time"`   // ms
	Definite  bool     `json:"definite,omitempty"`
	Words     []asrWrd `json:"words,omitempty"`
}

type asrWrd struct {
	Text      string `json:"text"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

// readMultipartAudio extracts the audio bytes + filename out of the gin
// request's multipart form. Mirrors the pattern in
// `relay/channel/cloudflare/adaptor.go::ConvertAudioRequest`.
//
// Returns the bytes, the original filename (best-effort, may be empty), and
// any parse error.
func readMultipartAudio(c *gin.Context) ([]byte, string, error) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		return nil, "", fmt.Errorf("multipart 'file' part is required: %w", err)
	}
	defer file.Close()

	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, file); err != nil {
		return nil, "", fmt.Errorf("read 'file' part: %w", err)
	}
	filename := ""
	if header != nil {
		filename = header.Filename
	}
	return buf.Bytes(), filename, nil
}

// asrConvertContextKey stashes intermediate state (file bytes, filename,
// language) between ConvertAudioRequest and DoResponse, since the gin-driven
// API splits these into separate adaptor methods.
const contextKeyASRRequest = "volcengine_asr_request"

type asrContext struct {
	AudioBytes []byte
	Filename   string
	Language   string // "zh-CN" by default; pulled from form param if present
}

// convertAudioRequestForASR is called by Adaptor.ConvertAudioRequest when the
// relay mode is RelayModeAudioTranscription. It does NOT yet upload to VOD —
// that happens in DoRequest, where we have a clean place to do network I/O
// and surface upstream errors.
func convertAudioRequestForASR(c *gin.Context, _ *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	audioBytes, filename, err := readMultipartAudio(c)
	if err != nil {
		return nil, err
	}
	if len(audioBytes) == 0 {
		return nil, errors.New("uploaded audio file is empty")
	}

	// Language: prefer form param `language`, fallback to default "zh-CN".
	// (OpenAI's whisper accepts ISO-639-1; Doubao wants "zh-CN" / "en-US" etc.)
	language := strings.TrimSpace(c.PostForm("language"))
	if language == "" {
		// dto.AudioRequest.Language is json.RawMessage; try to extract as string.
		if len(request.Language) > 0 {
			var lang string
			if err := json.Unmarshal(request.Language, &lang); err == nil {
				language = strings.TrimSpace(lang)
			}
		}
	}
	if language == "" {
		language = "zh-CN"
	}

	c.Set(contextKeyASRRequest, &asrContext{
		AudioBytes: audioBytes,
		Filename:   filename,
		Language:   language,
	})

	// Return an empty reader: the actual upstream call is orchestrated by
	// the ASR-specific DoRequest path (we don't fit cleanly into newapi's
	// stock "convert→reader→DoRequest pipes the reader" assumption since
	// the ASR submit body is built AFTER the VOD upload completes).
	return bytes.NewReader(nil), nil
}

// doASRRequest runs the full ASR pipeline (upload → submit → poll). It is
// invoked from Adaptor.DoRequest when relay mode is transcription.
//
// Returns a synthetic *http.Response wrapping the final ASR query body, so
// the standard relay pipeline's downstream phases (DoResponse → write to
// client) just work. We synthesize 200 OK with `application/json` body when
// the ASR completes successfully; failures bubble up as plain Go errors so
// the relay layer maps them to a 502.
func doASRRequest(c *gin.Context, info *relaycommon.RelayInfo, getOtherInfo func(channelID int) (map[string]interface{}, error)) (*http.Response, error) {
	ctxVal, ok := c.Get(contextKeyASRRequest)
	if !ok {
		return nil, errors.New("ASR context not initialized; ConvertAudioRequest did not run")
	}
	asrCtx, ok := ctxVal.(*asrContext)
	if !ok || asrCtx == nil {
		return nil, errors.New("ASR context has wrong type")
	}

	// 1. Auth: split <APP_ID>|<ACCESS_TOKEN>.
	appID, accessToken, err := parseVolcengineAuth(info.ApiKey)
	if err != nil {
		return nil, fmt.Errorf("invalid channel api_key (need APP_ID|ACCESS_TOKEN): %w", err)
	}

	// 2. VOD credentials live in OtherInfo (not in the channel Key, which is
	// reserved for the service-level APP_ID|ACCESS_TOKEN).
	creds, credErr := LoadVodCredentialsFromRelayInfo(info, getOtherInfo)
	if credErr != nil {
		return nil, credErr
	}
	if creds.PlaybackDomain == "" {
		return nil, errors.New("vod_playback_domain not configured on channel (required for ASR to fetch the uploaded audio)")
	}

	// 3. Upload audio to VOD.
	ext := inferExtFromFilename(asrCtx.Filename)
	contentType := inferContentTypeFromFilename(asrCtx.Filename)
	uploadOpts := VodUploadOpts{
		SpaceName:     creds.Space,
		Region:        creds.Region,
		AccessKey:     creds.AccessKey,
		SecretKey:     creds.SecretKey,
		FileExtension: ext,
		FileName:      asrCtx.Filename,
	}
	uploadResult, uploadErr := asrUploadFn(asrCtx.AudioBytes, uploadOpts, contentType)
	if uploadErr != nil {
		return nil, fmt.Errorf("VOD upload failed: %w", uploadErr)
	}

	// 4. Build the public audio URL. We rely on the playback domain the
	// operator configured; URL-auth signing is intentionally NOT done here
	// because (a) ASR can fetch unsigned for VOD spaces where url-auth is
	// off, and (b) signed URLs hardcode an expiry, which complicates retries.
	// If url-auth is mandatory on the operator's VOD space, switch the
	// space's URL-auth setting to "off" (or extend this code to sign).
	scheme := creds.PlaybackScheme
	audioURL := fmt.Sprintf("%s://%s/%s", scheme, creds.PlaybackDomain, strings.TrimPrefix(uploadResult.FileName, "/"))

	// 5. Submit to Doubao ASR.
	taskID, submitErr := asrSubmit(c.Request.Context(), accessToken, appID, audioURL, asrCtx, asrAudioFormatFor(asrCtx.Filename))
	if submitErr != nil {
		return nil, submitErr
	}
	if taskID == "" {
		return nil, errors.New("Doubao ASR submit returned empty task id (X-Api-Request-Id header missing)")
	}

	// 6. Poll until terminal.
	result, pollErr := asrPoll(c.Request.Context(), accessToken, taskID)
	if pollErr != nil {
		return nil, pollErr
	}

	// 7. Translate to OpenAI Whisper-compatible verbose JSON.
	whisperJSON := utterancesToWhisperJSON(result, asrCtx.Language)
	jsonBytes, err := json.Marshal(whisperJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal whisper response: %w", err)
	}

	// 8. Wrap in a synthetic http.Response so the downstream relay flow can
	// read body / status uniformly.
	synthetic := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(jsonBytes)),
	}
	synthetic.Header.Set("Content-Type", "application/json")
	return synthetic, nil
}

// asrSubmit POSTs to /api/v3/auc/bigmodel/submit. Returns the task id (we
// use the X-Api-Request-Id header value we sent, which doubles as the query
// key per Doubao's protocol).
func asrSubmit(ctx context.Context, accessToken, appID, audioURL string, asrCtx *asrContext, format string) (string, error) {
	reqID := uuid.NewString()

	body := asrRequest{
		User: asrUserInfo{UID: fallbackUID(appID)},
		Audio: asrAudioInfo{
			URL:      audioURL,
			Format:   format,
			Language: asrCtx.Language,
		},
		Request: asrRequestInfo{
			ModelName:      "bigmodel",
			EnableITN:      true,
			EnablePunc:     true,
			ShowUtterances: true,
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal ASR submit body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, asrSubmitEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("new ASR submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", accessToken)
	req.Header.Set("X-Api-Resource-Id", asrResourceID)
	req.Header.Set("X-Api-Request-Id", reqID)
	req.Header.Set("X-Api-Sequence", "-1")
	if appID != "" {
		req.Header.Set("X-Api-App-Key", appID)
	}

	resp, err := asrHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ASR submit network error: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	statusCode := resp.Header.Get("X-Api-Status-Code")
	if resp.StatusCode >= 400 || (statusCode != "" && statusCode != asrStatusSuccess && statusCode != asrStatusProcessing && statusCode != asrStatusQueued) {
		return "", asrErrorFromResponse(resp.StatusCode, statusCode, respBody)
	}
	// Per protocol, the request id we sent IS the query handle. Some payloads
	// also echo a `task_id` field; prefer it when present (defensive).
	if len(respBody) > 0 {
		var parsed asrSubmitResponse
		if err := json.Unmarshal(respBody, &parsed); err == nil && parsed.TaskID != "" {
			return parsed.TaskID, nil
		}
	}
	return reqID, nil
}

// asrPoll calls /api/v3/auc/bigmodel/query every 2 seconds until terminal.
// Returns the final query body on success (X-Api-Status-Code = 20000000).
func asrPoll(ctx context.Context, accessToken, taskID string) (*asrQueryResponse, error) {
	deadline := asrNowFn().Add(time.Duration(asrMaxPollSecs) * time.Second)
	for {
		if asrNowFn().After(deadline) {
			return nil, fmt.Errorf("Doubao ASR polling timed out after %ds", asrMaxPollSecs)
		}
		resp, body, statusCode, err := asrQueryOnce(ctx, accessToken, taskID)
		if err != nil {
			return nil, err
		}
		switch statusCode {
		case asrStatusSuccess:
			parsed := &asrQueryResponse{}
			if err := json.Unmarshal(body, parsed); err != nil {
				return nil, fmt.Errorf("ASR query: parse response: %w (body=%s)", err, truncate(string(body), 400))
			}
			return parsed, nil
		case asrStatusProcessing, asrStatusQueued:
			// keep polling
		case asrStatusSilent:
			// Silent audio: success-with-empty-result.
			return &asrQueryResponse{}, nil
		default:
			// Terminal failure; bubble up with the upstream code.
			return nil, asrErrorFromResponse(resp, statusCode, body)
		}
		asrSleepFn(time.Duration(asrPollIntervalSecs) * time.Second)
	}
}

func asrQueryOnce(ctx context.Context, accessToken, taskID string) (int, []byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, asrQueryEndpoint, bytes.NewReader([]byte("{}")))
	if err != nil {
		return 0, nil, "", fmt.Errorf("new ASR query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", accessToken)
	req.Header.Set("X-Api-Resource-Id", asrResourceID)
	req.Header.Set("X-Api-Request-Id", taskID)
	req.Header.Set("X-Api-Sequence", "-1")

	resp, err := asrHTTPClient.Do(req)
	if err != nil {
		return 0, nil, "", fmt.Errorf("ASR query network error: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	statusCode := resp.Header.Get("X-Api-Status-Code")
	return resp.StatusCode, respBody, statusCode, nil
}

// utterancesToWhisperJSON turns the Doubao ASR result into an OpenAI Whisper-
// compatible verbose JSON. We pin language to the short ISO code (e.g. "zh")
// since the OpenAI shape uses that, not the BCP-47 "zh-CN" we send to Doubao.
func utterancesToWhisperJSON(query *asrQueryResponse, requestedLanguage string) *dto.WhisperVerboseJSONResponse {
	if query == nil {
		return &dto.WhisperVerboseJSONResponse{Text: ""}
	}
	resp := &dto.WhisperVerboseJSONResponse{
		Task:     "transcribe",
		Language: shortLanguage(requestedLanguage),
		Duration: query.AudioInfo.Duration,
		Text:     query.Result.Text,
	}
	if len(query.Result.Utterances) == 0 {
		return resp
	}
	segments := make([]dto.Segment, 0, len(query.Result.Utterances))
	for i, u := range query.Result.Utterances {
		segments = append(segments, dto.Segment{
			Id:    i,
			Start: float64(u.StartTime) / 1000.0,
			End:   float64(u.EndTime) / 1000.0,
			Text:  u.Text,
		})
	}
	resp.Segments = segments
	if resp.Text == "" {
		// Doubao sometimes omits the joined text; reconstruct from utterances.
		var sb strings.Builder
		for _, u := range query.Result.Utterances {
			sb.WriteString(u.Text)
		}
		resp.Text = sb.String()
	}
	return resp
}

// utterancesToSRT renders utterances as an SRT-formatted string. Exposed so
// callers downstream (CCS frontend) that prefer SRT over the OpenAI-style
// segments array can request it via response_format=srt. Not wired into the
// main response path yet — we return the verbose JSON by default; SRT is
// available as a utility.
func utterancesToSRT(query *asrQueryResponse) string {
	if query == nil || len(query.Result.Utterances) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, u := range query.Result.Utterances {
		fmt.Fprintf(&sb, "%d\n", i+1)
		fmt.Fprintf(&sb, "%s --> %s\n", formatSRTTimestamp(u.StartTime), formatSRTTimestamp(u.EndTime))
		fmt.Fprintf(&sb, "%s\n\n", u.Text)
	}
	return sb.String()
}

func formatSRTTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	hours := ms / 3600000
	rem := ms % 3600000
	minutes := rem / 60000
	rem = rem % 60000
	seconds := rem / 1000
	millis := rem % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, millis)
}

func shortLanguage(lang string) string {
	if lang == "" {
		return ""
	}
	if idx := strings.Index(lang, "-"); idx > 0 {
		return strings.ToLower(lang[:idx])
	}
	return strings.ToLower(lang)
}

// asrAudioFormatFor returns the value to put in `audio.format` for the
// Doubao ASR submit request.
func asrAudioFormatFor(filename string) string {
	return inferAudioFormatFromFilename(filename)
}

func fallbackUID(appID string) string {
	if appID != "" {
		return appID
	}
	return "newapi"
}

func asrErrorFromResponse(httpStatus int, statusCode string, body []byte) error {
	snippet := truncate(string(body), 400)
	if statusCode != "" {
		return fmt.Errorf("Doubao ASR upstream error: X-Api-Status-Code=%s HTTP=%d body=%s", statusCode, httpStatus, snippet)
	}
	return fmt.Errorf("Doubao ASR upstream error: HTTP=%d body=%s", httpStatus, snippet)
}

// handleASRResponse is invoked by Adaptor.DoResponse when relay mode is
// transcription. The "response" here is the synthetic one we built inside
// doASRRequest; this function streams it to the client and reports usage.
func handleASRResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if resp == nil {
		return nil, types.NewErrorWithStatusCode(
			errors.New("ASR: nil response"),
			types.ErrorCodeBadResponse,
			http.StatusInternalServerError,
		)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("ASR: read response body: %w", err),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(body)

	// Billing: ASR is priced by audio duration. We express the recognized
	// audio length (in whole seconds, rounded up) as PromptTokens so newapi's
	// standard token × ModelRatio machinery produces a per-second charge.
	//
	//   charge_USD = ceil(duration_seconds) × ModelRatio × QuotaPerUnit^-1 × groupRatio
	//
	// The operator sets ModelRatio so that 1 "token" (= 1 second) costs the
	// desired per-second price. See the channel-config report for the exact
	// number. When the upstream omits duration (e.g. silent audio), we fall
	// back to a 1-second minimum so the request is never billed at 0.
	var parsed dto.WhisperVerboseJSONResponse
	_ = json.Unmarshal(body, &parsed)
	durationSeconds := int(parsed.Duration)
	if float64(durationSeconds) < parsed.Duration {
		durationSeconds++ // round up partial seconds
	}
	if durationSeconds < 1 {
		durationSeconds = 1
	}

	usage := &dto.Usage{
		PromptTokens:     durationSeconds,
		CompletionTokens: 0,
		TotalTokens:      durationSeconds,
	}
	return usage, nil
}

// asrRequestSupportsMultipart guards against accidentally routing JSON
// transcription requests through this path. Volcengine ASR is multipart-only.
func asrRequestSupportsMultipart(c *gin.Context) bool {
	return strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data")
}
