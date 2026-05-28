// Volcengine VOD upload bridge (shared by Doubao ASR + volcvod task multipart input).
//
// Lives in the `volcvod` package alongside the SigV4 signer so callers in
// either direction (relay/channel/volcengine -> ASR, relay/channel/task/volcvod
// -> multipart auto-upload) can use it without a circular import.
//
// Three-step flow (mirrors the TS implementation at
// `~/Code/keli-canvas/server/lib/volcengine/vodUploader.ts`):
//  1. ApplyUploadInfo  (GET, Version=2022-01-01) → UploadAddress (SessionKey + StoreUri + Auth + UploadHost)
//  2. TOS direct PUT   → upload bytes using the pre-signed Authorization header verbatim
//  3. CommitUploadInfo (GET, Version=2022-01-01) → finalize, returns Vid + (optional) SourceInfo
//
// v1 limits (matching TS):
//   - Single-PUT only; files > 20 MiB return ErrFileTooLarge. Multipart not yet implemented.
package volcvod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	vodUploadEndpoint        = "vod.volcengineapi.com"
	vodUploadVersion         = "2022-01-01"
	VodSinglePutLimitBytes   = 20 * 1024 * 1024 // 20 MiB
	vodUploadHTTPTimeoutSecs = 90
)

// VodUploadError captures the upstream failure code + a short detail snippet
// suitable for logs (without leaking the SK / Authorization header).
type VodUploadError struct {
	Code    string
	Message string
	Status  int
	Detail  string
}

func (e *VodUploadError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("vod_upload: %s (status=%d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("vod_upload: %s: %s", e.Code, e.Message)
}

func newVodUploadError(code, message string) *VodUploadError {
	return &VodUploadError{Code: code, Message: message}
}

// VodUploadOpts collects the Volcengine credentials + space routing fields
// for one upload call.
type VodUploadOpts struct {
	SpaceName     string
	Region        string // typically "cn-north-1"
	AccessKey     string
	SecretKey     string
	FileExtension string // ".mp3" / ".mp4" / etc. (with leading dot, optional)
	FileName      string // optional override; informational only
}

// VodUploadResult is the post-Commit outcome the caller needs to feed into
// downstream APIs (e.g. ASR needs Vid + the FileName so it can build the
// public playback URL).
type VodUploadResult struct {
	Vid      string
	FileName string  // <playback-domain>/FileName resolves to the asset
	Duration float64 // seconds (when SourceInfo reports it)
	Width    int
	Height   int
	FileSize int64
}

// HTTPDoer is the minimal contract we need from an http.Client. Tests inject
// a fake; production callers use a normal *http.Client.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultVodHTTPClient is the *http.Client used by UploadMediaToVOD when no
// override is supplied. Tests swap this via SetVodHTTPClientForTests.
var defaultVodHTTPClient HTTPDoer = &http.Client{Timeout: time.Duration(vodUploadHTTPTimeoutSecs) * time.Second}

// SetVodHTTPClientForTests overrides the default HTTP client used by the
// upload + commit / TOS PUT calls. Tests call this with a deterministic fake.
// Returns the previous client so tests can restore it.
func SetVodHTTPClientForTests(client HTTPDoer) HTTPDoer {
	prev := defaultVodHTTPClient
	defaultVodHTTPClient = client
	return prev
}

type applyUploadResponse struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"ResponseMetadata"`
	Result struct {
		Data struct {
			UploadAddress struct {
				SessionKey string `json:"SessionKey"`
				StoreInfos []struct {
					StoreUri string `json:"StoreUri"`
					Auth     string `json:"Auth"`
				} `json:"StoreInfos"`
				UploadHosts []string `json:"UploadHosts"`
			} `json:"UploadAddress"`
		} `json:"Data"`
	} `json:"Result"`
}

type commitUploadResponse struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"ResponseMetadata"`
	Result struct {
		Data struct {
			Vid        string `json:"Vid"`
			SourceInfo struct {
				FileName string  `json:"FileName"`
				Duration float64 `json:"Duration"`
				Width    int     `json:"Width"`
				Height   int     `json:"Height"`
				Size     int64   `json:"Size"`
			} `json:"SourceInfo"`
		} `json:"Data"`
	} `json:"Result"`
}

// UploadMediaToVOD uploads `body` to Volcengine VOD and returns the resulting
// Vid (+ metadata).
//
// Contract / failure modes:
//   - Empty body  →  VodUploadError{Code:"empty_file"}
//   - body > 20MiB → VodUploadError{Code:"file_too_large_for_single_put"}
//   - Upstream errors map to {Code:"apply_upload_<code>", "tos_put_<...>", "commit_upload_<code>"}
//
// `contentType` (e.g. "audio/mpeg") is informational; propagated as TOS PUT
// `Content-Type` hint.
func UploadMediaToVOD(body []byte, opts VodUploadOpts, contentType string) (*VodUploadResult, error) {
	if len(body) == 0 {
		return nil, newVodUploadError("empty_file", "cannot upload empty body")
	}
	if int64(len(body)) > int64(VodSinglePutLimitBytes) {
		return nil, &VodUploadError{
			Code:    "file_too_large_for_single_put",
			Message: fmt.Sprintf("VOD single-PUT supports up to %d bytes (got %d); multipart not implemented", VodSinglePutLimitBytes, len(body)),
		}
	}
	if opts.AccessKey == "" || opts.SecretKey == "" {
		return nil, newVodUploadError("missing_credentials", "VOD access key / secret key not configured on channel")
	}
	if opts.SpaceName == "" {
		return nil, newVodUploadError("missing_space", "VOD space name not configured on channel")
	}
	region := opts.Region
	if region == "" {
		region = "cn-north-1"
	}

	// 1. ApplyUploadInfo
	applyResp, err := applyUploadInfo(int64(len(body)), opts, region)
	if err != nil {
		return nil, err
	}
	addr := applyResp.Result.Data.UploadAddress
	if addr.SessionKey == "" || len(addr.StoreInfos) == 0 || len(addr.UploadHosts) == 0 {
		return nil, &VodUploadError{
			Code:    "apply_upload_invalid",
			Message: "ApplyUploadInfo response missing required fields (SessionKey / StoreInfos / UploadHosts)",
		}
	}
	store := addr.StoreInfos[0]
	if store.StoreUri == "" || store.Auth == "" {
		return nil, &VodUploadError{
			Code:    "apply_upload_invalid",
			Message: "ApplyUploadInfo: StoreInfos[0].StoreUri / Auth missing",
		}
	}
	uploadHost := addr.UploadHosts[0]

	// 2. TOS PUT (pre-signed Authorization is used verbatim — do NOT sign again)
	if err := tosSinglePut(uploadHost, store.StoreUri, store.Auth, body, contentType); err != nil {
		return nil, err
	}

	// 3. CommitUploadInfo (Functions=[{GetMeta}] forces synchronous metadata extraction)
	commit, err := commitUploadInfo(addr.SessionKey, opts, region)
	if err != nil {
		return nil, err
	}
	if commit.Result.Data.Vid == "" {
		return nil, &VodUploadError{
			Code:    "commit_upload_no_vid",
			Message: "CommitUploadInfo did not return a Vid",
		}
	}
	res := &VodUploadResult{
		Vid:      commit.Result.Data.Vid,
		FileName: commit.Result.Data.SourceInfo.FileName,
		Duration: commit.Result.Data.SourceInfo.Duration,
		Width:    commit.Result.Data.SourceInfo.Width,
		Height:   commit.Result.Data.SourceInfo.Height,
		FileSize: commit.Result.Data.SourceInfo.Size,
	}
	if res.FileSize == 0 {
		res.FileSize = int64(len(body))
	}
	return res, nil
}

func applyUploadInfo(fileSize int64, opts VodUploadOpts, region string) (*applyUploadResponse, error) {
	query := url.Values{}
	query.Set("Action", "ApplyUploadInfo")
	query.Set("Version", vodUploadVersion)
	query.Set("SpaceName", opts.SpaceName)
	query.Set("FileType", "video")
	query.Set("FileSize", fmt.Sprintf("%d", fileSize))
	if opts.FileExtension != "" {
		query.Set("FileExtension", opts.FileExtension)
	}
	if opts.FileName != "" {
		query.Set("FileName", opts.FileName)
	}

	baseHdrs := map[string]string{
		"host":         vodUploadEndpoint,
		"content-type": "application/json",
	}
	signed, err := SignRequest(http.MethodGet, vodUploadEndpoint, "/", query, baseHdrs, nil,
		opts.AccessKey, opts.SecretKey, region, "vod")
	if err != nil {
		return nil, &VodUploadError{Code: "apply_upload_sign_failed", Message: err.Error()}
	}

	fullURL := fmt.Sprintf("https://%s/?%s", vodUploadEndpoint, query.Encode())
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, &VodUploadError{Code: "apply_upload_new_request_failed", Message: err.Error()}
	}
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	resp, err := defaultVodHTTPClient.Do(req)
	if err != nil {
		return nil, &VodUploadError{Code: "apply_upload_network_error", Message: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &VodUploadError{
			Code:    "apply_upload_http_error",
			Message: fmt.Sprintf("ApplyUploadInfo HTTP %d", resp.StatusCode),
			Status:  resp.StatusCode,
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	parsed := &applyUploadResponse{}
	if err := json.Unmarshal(respBody, parsed); err != nil {
		return nil, &VodUploadError{
			Code:    "apply_upload_parse_error",
			Message: "ApplyUploadInfo returned non-JSON",
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	if parsed.ResponseMetadata.Error != nil && parsed.ResponseMetadata.Error.Code != "" {
		return nil, &VodUploadError{
			Code:    "apply_upload_" + parsed.ResponseMetadata.Error.Code,
			Message: fmt.Sprintf("ApplyUploadInfo error: %s %s", parsed.ResponseMetadata.Error.Code, parsed.ResponseMetadata.Error.Message),
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	return parsed, nil
}

func tosSinglePut(uploadHost, storeURI, auth string, body []byte, contentType string) error {
	parts := strings.Split(storeURI, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	encodedPath := strings.Join(parts, "/")
	fullURL := fmt.Sprintf("https://%s/%s", uploadHost, encodedPath)

	sum := crc32.ChecksumIEEE(body)
	crcHex := fmt.Sprintf("%08x", sum)

	req, err := http.NewRequest(http.MethodPut, fullURL, bytes.NewReader(body))
	if err != nil {
		return &VodUploadError{Code: "tos_put_new_request_failed", Message: err.Error()}
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-CRC32", crcHex)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = int64(len(body))

	resp, err := defaultVodHTTPClient.Do(req)
	if err != nil {
		return &VodUploadError{Code: "tos_put_network_error", Message: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &VodUploadError{
			Code:    "tos_put_http_error",
			Message: fmt.Sprintf("TOS PUT HTTP %d", resp.StatusCode),
			Status:  resp.StatusCode,
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	if len(respBody) > 0 {
		var parsed struct {
			Success *int `json:"success,omitempty"`
		}
		if err := json.Unmarshal(respBody, &parsed); err == nil && parsed.Success != nil && *parsed.Success != 0 {
			return &VodUploadError{
				Code:    "tos_put_bad_success",
				Message: fmt.Sprintf("TOS PUT returned success=%d", *parsed.Success),
				Detail:  truncateStr(string(respBody), 800),
			}
		}
	}
	return nil
}

func commitUploadInfo(sessionKey string, opts VodUploadOpts, region string) (*commitUploadResponse, error) {
	query := url.Values{}
	query.Set("Action", "CommitUploadInfo")
	query.Set("Version", vodUploadVersion)
	query.Set("SpaceName", opts.SpaceName)
	query.Set("SessionKey", sessionKey)
	query.Set("Functions", `[{"Name":"GetMeta"}]`)

	baseHdrs := map[string]string{
		"host":         vodUploadEndpoint,
		"content-type": "application/json",
	}
	signed, err := SignRequest(http.MethodGet, vodUploadEndpoint, "/", query, baseHdrs, nil,
		opts.AccessKey, opts.SecretKey, region, "vod")
	if err != nil {
		return nil, &VodUploadError{Code: "commit_upload_sign_failed", Message: err.Error()}
	}

	fullURL := fmt.Sprintf("https://%s/?%s", vodUploadEndpoint, query.Encode())
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, &VodUploadError{Code: "commit_upload_new_request_failed", Message: err.Error()}
	}
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	resp, err := defaultVodHTTPClient.Do(req)
	if err != nil {
		return nil, &VodUploadError{Code: "commit_upload_network_error", Message: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &VodUploadError{
			Code:    "commit_upload_http_error",
			Message: fmt.Sprintf("CommitUploadInfo HTTP %d", resp.StatusCode),
			Status:  resp.StatusCode,
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	parsed := &commitUploadResponse{}
	if err := json.Unmarshal(respBody, parsed); err != nil {
		return nil, &VodUploadError{
			Code:    "commit_upload_parse_error",
			Message: "CommitUploadInfo returned non-JSON",
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	if parsed.ResponseMetadata.Error != nil && parsed.ResponseMetadata.Error.Code != "" {
		return nil, &VodUploadError{
			Code:    "commit_upload_" + parsed.ResponseMetadata.Error.Code,
			Message: fmt.Sprintf("CommitUploadInfo error: %s %s", parsed.ResponseMetadata.Error.Code, parsed.ResponseMetadata.Error.Message),
			Detail:  truncateStr(string(respBody), 800),
		}
	}
	return parsed, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// InferContentTypeFromFilename returns a best-effort MIME hint based on the
// file extension.
func InferContentTypeFromFilename(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(lower, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(lower, ".aac"):
		return "audio/aac"
	case strings.HasSuffix(lower, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(lower, ".flac"):
		return "audio/flac"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".webm"):
		return "video/webm"
	case strings.HasSuffix(lower, ".mov"):
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

// InferExtFromFilename returns the file extension (with leading dot) or "".
func InferExtFromFilename(filename string) string {
	idx := strings.LastIndex(filename, ".")
	if idx < 0 || idx == len(filename)-1 {
		return ""
	}
	return strings.ToLower(filename[idx:])
}

// InferAudioFormatFromFilename maps the file extension to a Doubao ASR
// `audio.format` field value ("mp3" / "wav" / ...). Defaults to "mp3".
func InferAudioFormatFromFilename(filename string) string {
	ext := strings.TrimPrefix(InferExtFromFilename(filename), ".")
	switch ext {
	case "mp3", "wav", "aac", "ogg", "flac", "m4a", "mp4", "webm", "mov":
		return ext
	default:
		return "mp3"
	}
}
