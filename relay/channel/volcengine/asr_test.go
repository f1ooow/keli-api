package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/task/volcvod"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestFormatSRTTimestamp(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "00:00:00,000"},
		{500, "00:00:00,500"},
		{1500, "00:00:01,500"},
		{61500, "00:01:01,500"},
		{3661500, "01:01:01,500"},
		{-100, "00:00:00,000"},
	}
	for _, c := range cases {
		got := formatSRTTimestamp(c.ms)
		if got != c.want {
			t.Errorf("formatSRTTimestamp(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestUtterancesToSRT(t *testing.T) {
	q := &asrQueryResponse{}
	q.Result.Utterances = []asrUtterance{
		{Text: "你好世界", StartTime: 0, EndTime: 1500, Definite: true},
		{Text: "这是字幕", StartTime: 1500, EndTime: 3200, Definite: true},
	}
	srt := utterancesToSRT(q)
	wantParts := []string{
		"1\n00:00:00,000 --> 00:00:01,500\n你好世界\n",
		"2\n00:00:01,500 --> 00:00:03,200\n这是字幕\n",
	}
	for _, w := range wantParts {
		if !strings.Contains(srt, w) {
			t.Errorf("SRT missing %q\nGot:\n%s", w, srt)
		}
	}
}

func TestUtterancesToSRT_Empty(t *testing.T) {
	if got := utterancesToSRT(nil); got != "" {
		t.Errorf("nil → %q, want empty", got)
	}
	if got := utterancesToSRT(&asrQueryResponse{}); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
}

func TestUtterancesToWhisperJSON(t *testing.T) {
	q := &asrQueryResponse{}
	q.AudioInfo.Duration = 3200 // doubao audio_info.duration is milliseconds
	q.Result.Text = "你好世界 这是字幕"
	q.Result.Utterances = []asrUtterance{
		{Text: "你好世界", StartTime: 0, EndTime: 1500, Definite: true},
		{Text: "这是字幕", StartTime: 1500, EndTime: 3200, Definite: true},
	}
	out := utterancesToWhisperJSON(q, "zh-CN")
	if out.Language != "zh" {
		t.Errorf("Language = %q, want zh", out.Language)
	}
	if out.Duration != 3.2 {
		t.Errorf("Duration = %v", out.Duration)
	}
	if out.Text != "你好世界 这是字幕" {
		t.Errorf("Text = %q", out.Text)
	}
	if len(out.Segments) != 2 {
		t.Fatalf("Segments len = %d", len(out.Segments))
	}
	if out.Segments[0].Start != 0.0 || out.Segments[0].End != 1.5 {
		t.Errorf("Segment 0 = (%v, %v)", out.Segments[0].Start, out.Segments[0].End)
	}
	if out.Segments[1].Start != 1.5 || out.Segments[1].End != 3.2 {
		t.Errorf("Segment 1 = (%v, %v)", out.Segments[1].Start, out.Segments[1].End)
	}
}

func TestUtterancesToWhisperJSON_TextReconstruction(t *testing.T) {
	// When `Result.Text` is empty, the helper should join utterances.
	q := &asrQueryResponse{}
	q.Result.Utterances = []asrUtterance{
		{Text: "a"},
		{Text: "b"},
		{Text: "c"},
	}
	out := utterancesToWhisperJSON(q, "en-US")
	if out.Text != "abc" {
		t.Errorf("Text reconstruction = %q, want abc", out.Text)
	}
	if out.Language != "en" {
		t.Errorf("Language = %q, want en", out.Language)
	}
}

func TestShortLanguage(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"zh-CN", "zh"},
		{"zh", "zh"},
		{"en-US", "en"},
		{"EN", "en"},
	}
	for _, c := range cases {
		if got := shortLanguage(c.in); got != c.want {
			t.Errorf("shortLanguage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Build a fake gin.Context backed by an httptest recorder with a multipart
// audio payload. Used by ConvertAudioRequest tests.
func newMultipartCtx(t *testing.T, filename string, body []byte, extraFields map[string]string) *gin.Context {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	if filename != "" {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(body)
	}
	for k, v := range extraFields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	return c
}

func TestConvertAudioRequestForASR_HappyPath(t *testing.T) {
	c := newMultipartCtx(t, "speech.mp3", []byte("FAKE-AUDIO-BYTES"), map[string]string{
		"model":    "doubao-asr-2.0",
		"language": "zh-CN",
	})
	info := &relaycommon.RelayInfo{}
	reader, err := convertAudioRequestForASR(c, info, dto.AudioRequest{Model: "doubao-asr-2.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reader == nil {
		t.Error("expected non-nil io.Reader (empty body OK)")
	}
	ctxVal, ok := c.Get(contextKeyASRRequest)
	if !ok {
		t.Fatal("ASR context not set")
	}
	asrCtx, ok := ctxVal.(*asrContext)
	if !ok {
		t.Fatalf("ASR context wrong type: %T", ctxVal)
	}
	if !bytes.Equal(asrCtx.AudioBytes, []byte("FAKE-AUDIO-BYTES")) {
		t.Errorf("audio bytes mismatch: %q", asrCtx.AudioBytes)
	}
	if asrCtx.Filename != "speech.mp3" {
		t.Errorf("filename = %q", asrCtx.Filename)
	}
	if asrCtx.Language != "zh-CN" {
		t.Errorf("language = %q", asrCtx.Language)
	}
}

func TestConvertAudioRequestForASR_MissingFile(t *testing.T) {
	// multipart without a file part
	c := newMultipartCtx(t, "", nil, map[string]string{"model": "doubao-asr-2.0"})
	info := &relaycommon.RelayInfo{}
	_, err := convertAudioRequestForASR(c, info, dto.AudioRequest{Model: "doubao-asr-2.0"})
	if err == nil {
		t.Fatal("expected error for missing file part")
	}
	if !strings.Contains(err.Error(), "multipart 'file' part is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConvertAudioRequestForASR_EmptyFile(t *testing.T) {
	c := newMultipartCtx(t, "speech.mp3", []byte{}, map[string]string{"model": "doubao-asr-2.0"})
	info := &relaycommon.RelayInfo{}
	_, err := convertAudioRequestForASR(c, info, dto.AudioRequest{Model: "doubao-asr-2.0"})
	if err == nil {
		t.Fatal("expected error for empty audio")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConvertAudioRequestForASR_DefaultLanguage(t *testing.T) {
	c := newMultipartCtx(t, "x.mp3", []byte("xx"), map[string]string{"model": "doubao-asr-2.0"})
	info := &relaycommon.RelayInfo{}
	_, err := convertAudioRequestForASR(c, info, dto.AudioRequest{Model: "doubao-asr-2.0"})
	if err != nil {
		t.Fatal(err)
	}
	asrCtx := c.MustGet(contextKeyASRRequest).(*asrContext)
	if asrCtx.Language != "zh-CN" {
		t.Errorf("default language = %q, want zh-CN", asrCtx.Language)
	}
}

// asrFakeServer hosts a Doubao ASR submit+query mock with configurable
// X-Api-Status-Code progression for poll tests.
func TestDoASRRequest_FullPipeline(t *testing.T) {
	// 1. Mock the Doubao ASR submit + query endpoints with a small
	//    state machine: first /query returns "processing"; second returns
	//    success with utterances.
	queryHits := 0
	asrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/submit"):
			// echo X-Api-Request-Id as accepted task id; status header = queued
			w.Header().Set("X-Api-Status-Code", asrStatusQueued)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		case strings.HasSuffix(req.URL.Path, "/query"):
			queryHits++
			if queryHits == 1 {
				w.Header().Set("X-Api-Status-Code", asrStatusProcessing)
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"message":"processing"}`))
				return
			}
			w.Header().Set("X-Api-Status-Code", asrStatusSuccess)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{
				"audio_info": {"duration": 2.5},
				"result": {
					"text": "你好世界",
					"utterances": [
						{"text": "你好", "start_time": 0, "end_time": 1200},
						{"text": "世界", "start_time": 1200, "end_time": 2500}
					]
				}
			}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer asrSrv.Close()

	// Point our package's asr endpoints at the mock by overriding the
	// transport. We do this by swapping the asrHTTPClient with one that
	// rewrites the host to the test server's host.
	origClient := asrHTTPClient
	defer func() { asrHTTPClient = origClient }()
	asrHTTPClient = &http.Client{Timeout: 5 * time.Second, Transport: &hostRewriter{toHost: asrSrv.Listener.Addr().String()}}

	// Speed up the poll loop
	origSleep := asrSleepFn
	asrSleepFn = func(time.Duration) {}
	defer func() { asrSleepFn = origSleep }()

	// 2. Mock the VOD upload to return a fixed Vid + FileName.
	origUpload := asrUploadFn
	asrUploadFn = func(body []byte, opts VodUploadOpts, _ string) (*VodUploadResult, error) {
		if len(body) == 0 {
			return nil, errors.New("empty body")
		}
		return &VodUploadResult{
			Vid:      "v-fake-123",
			FileName: "v/aa/foo.mp3",
		}, nil
	}
	defer func() { asrUploadFn = origUpload }()

	// 3. Build the gin context + RelayInfo with credentials.
	c := newMultipartCtx(t, "speech.mp3", []byte("FAKE"), map[string]string{
		"model":    "doubao-asr-2.0",
		"language": "zh-CN",
	})

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioTranscription,
	}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId: 99,
		ApiKey:    "APP123|TOKEN456",
	}

	// Run ConvertAudioRequest to seed the ASR context.
	if _, err := convertAudioRequestForASR(c, info, dto.AudioRequest{Model: "doubao-asr-2.0"}); err != nil {
		t.Fatal(err)
	}

	// Fake VOD credentials loader.
	getOtherInfo := func(channelID int) (map[string]interface{}, error) {
		return map[string]interface{}{
			"vod_ak":              "AKLT-x",
			"vod_sk":              "secret",
			"vod_space":           "kc",
			"vod_playback_domain": "test-cdn.example.com",
			"vod_playback_scheme": "https",
		}, nil
	}

	resp, err := doASRRequest(c, info, getOtherInfo)
	if err != nil {
		t.Fatalf("doASRRequest failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var parsed dto.WhisperVerboseJSONResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("response not whisper JSON: %v\nBody: %s", err, string(body))
	}
	if parsed.Text != "你好世界" {
		t.Errorf("Text = %q", parsed.Text)
	}
	if len(parsed.Segments) != 2 {
		t.Errorf("Segments len = %d", len(parsed.Segments))
	}
	if queryHits < 2 {
		t.Errorf("expected at least 2 query hits (processing + success), got %d", queryHits)
	}
}

func TestDoASRRequest_MissingChannelCreds(t *testing.T) {
	c := newMultipartCtx(t, "speech.mp3", []byte("FAKE"), map[string]string{"model": "doubao-asr-2.0"})
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioTranscription}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId: 99,
		ApiKey:    "APP123|TOKEN456",
	}
	if _, err := convertAudioRequestForASR(c, info, dto.AudioRequest{Model: "doubao-asr-2.0"}); err != nil {
		t.Fatal(err)
	}

	// Missing vod_ak in OtherInfo
	getOtherInfo := func(channelID int) (map[string]interface{}, error) {
		return map[string]interface{}{"vod_space": "kc"}, nil
	}
	_, err := doASRRequest(c, info, getOtherInfo)
	if err == nil {
		t.Fatal("expected ErrVodCredentialsMissing")
	}
	if !errors.Is(err, ErrVodCredentialsMissing) {
		t.Errorf("expected ErrVodCredentialsMissing, got %v", err)
	}
}

func TestDoASRRequest_MissingPlaybackDomain(t *testing.T) {
	c := newMultipartCtx(t, "speech.mp3", []byte("FAKE"), map[string]string{"model": "doubao-asr-2.0"})
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioTranscription}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId: 1,
		ApiKey:    "APP|TOKEN",
	}
	if _, err := convertAudioRequestForASR(c, info, dto.AudioRequest{}); err != nil {
		t.Fatal(err)
	}
	getOtherInfo := func(channelID int) (map[string]interface{}, error) {
		return map[string]interface{}{
			"vod_ak":    "ak",
			"vod_sk":    "sk",
			"vod_space": "kc",
			// playback_domain intentionally missing
		}, nil
	}
	_, err := doASRRequest(c, info, getOtherInfo)
	if err == nil || !strings.Contains(err.Error(), "vod_playback_domain") {
		t.Errorf("expected playback_domain error, got %v", err)
	}
}

// hostRewriter is a custom RoundTripper that swaps the Host of every outbound
// URL to a fixed test-server target. Used to redirect openspeech.bytedance.com
// calls to our httptest mock without touching DNS.
type hostRewriter struct {
	toHost string
}

func (h *hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = h.toHost
	req2.Host = h.toHost
	return http.DefaultTransport.RoundTrip(req2)
}

// Sanity: package can compile against the public dto types.
var _ context.Context = context.Background()

// Vet the asrRequest JSON shape stays Doubao-compatible.
func TestASRRequest_JSONShape(t *testing.T) {
	r := asrRequest{
		User:  asrUserInfo{UID: "appid"},
		Audio: asrAudioInfo{URL: "https://example.com/x.mp3", Format: "mp3", Language: "zh-CN"},
		Request: asrRequestInfo{
			ModelName:      "bigmodel",
			EnableITN:      true,
			EnablePunc:     true,
			ShowUtterances: true,
		},
	}
	out, _ := json.Marshal(r)
	want := []string{
		`"uid":"appid"`,
		`"url":"https://example.com/x.mp3"`,
		`"format":"mp3"`,
		`"language":"zh-CN"`,
		`"model_name":"bigmodel"`,
		`"enable_itn":true`,
		`"enable_punc":true`,
		`"show_utterances":true`,
	}
	for _, w := range want {
		if !strings.Contains(string(out), w) {
			t.Errorf("JSON missing %q\nGot: %s", w, string(out))
		}
	}
}

// TestHandleASRResponse_DurationBilling verifies the per-second billing:
// PromptTokens carries ceil(duration) seconds (≥1) so the standard
// token × ModelRatio machinery charges by audio length.
func TestHandleASRResponse_DurationBilling(t *testing.T) {
	cases := []struct {
		name       string
		duration   float64
		wantTokens int
	}{
		{"exact 10s", 10.0, 10},
		{"partial rounds up", 12.3, 13},
		{"tiny rounds to 1", 0.4, 1},
		{"zero falls back to 1", 0.0, 1},
		{"large", 305.0, 305},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whisper := dto.WhisperVerboseJSONResponse{Text: "x", Duration: tc.duration}
			jsonBytes, _ := json.Marshal(whisper)
			synthetic := &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader(jsonBytes)),
			}
			rr := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rr)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

			info := &relaycommon.RelayInfo{}
			usageAny, errOut := handleASRResponse(c, synthetic, info)
			if errOut != nil {
				t.Fatalf("handleASRResponse error: %v", errOut)
			}
			usage, ok := usageAny.(*dto.Usage)
			if !ok {
				t.Fatalf("usage wrong type: %T", usageAny)
			}
			if usage.PromptTokens != tc.wantTokens {
				t.Errorf("PromptTokens = %d, want %d (duration %v)", usage.PromptTokens, tc.wantTokens, tc.duration)
			}
			if usage.TotalTokens != tc.wantTokens {
				t.Errorf("TotalTokens = %d, want %d", usage.TotalTokens, tc.wantTokens)
			}
			if rr.Body.Len() == 0 {
				t.Error("response body not written to client")
			}
		})
	}
}

// Make sure asr.go re-exports from vod_uploader.go thread through.
func TestVodUploaderReExports(t *testing.T) {
	if inferContentTypeFromFilename("x.mp3") != volcvod.InferContentTypeFromFilename("x.mp3") {
		t.Error("inferContentTypeFromFilename wrapper out of sync")
	}
	if inferExtFromFilename("x.mp3") != volcvod.InferExtFromFilename("x.mp3") {
		t.Error("inferExtFromFilename wrapper out of sync")
	}
}
