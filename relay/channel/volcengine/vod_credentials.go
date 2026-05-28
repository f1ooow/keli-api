// Package volcengine - per-channel VOD credential resolution.
//
// VOD (upload bridge) and the higher-level service (TTS / ASR / video edit)
// share the same Volcengine account in practice, but the credentials live in
// two different shapes:
//
//   - Service credential (TTS app_id / ASR app_id + access_token) →
//     Channel.Key, format "<APP_ID>|<ACCESS_TOKEN>"
//   - VOD credential (AccessKey / SecretKey for SigV4) →
//     Channel.OtherInfo map (free-form JSON per channel)
//
// We deliberately do NOT extend `ChannelOtherSettings` (which would require a
// schema migration affecting all channels). The `OtherInfo` map is the
// existing extensibility hook for channel-specific config, and the admin UI
// already lets operators populate it.
//
// Expected OtherInfo keys (case-insensitive, snake_case + camelCase tolerated):
//
//	vod_ak              | vodAccessKey      → IAM Access Key ID (e.g. "AKLT...")
//	vod_sk              | vodSecretKey      → IAM Secret Key
//	vod_space           | vodSpace          → VOD SpaceName (account-scoped)
//	vod_region          | vodRegion         → default "cn-north-1"
//	vod_playback_domain | vodPlaybackDomain → e.g. "vod.robusta.top"
//	vod_playback_scheme | vodPlaybackScheme → "http" | "https" (default "https")
//
// All values are strings in the OtherInfo JSON. The loader returns a typed
// struct + a clear error when required fields are missing, so the caller
// (ASR / VOD task adaptor) can surface a useful message to the operator.
package volcengine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// defaultGetOtherInfo loads a channel's OtherInfo via the model layer. This is
// the production callback wired into ASR (doASRRequest / DoRequest); tests
// override it with an in-memory fake.
func defaultGetOtherInfo(channelID int) (map[string]interface{}, error) {
	ch, err := model.GetChannelById(channelID, false)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, fmt.Errorf("channel %d not found", channelID)
	}
	return ch.GetOtherInfo(), nil
}

// VodCredentials carries the VOD-specific fields extracted from a channel's
// OtherInfo blob.
type VodCredentials struct {
	AccessKey       string
	SecretKey       string
	Space           string
	Region          string // default "cn-north-1"
	PlaybackDomain  string // optional (only ASR needs it to build the audio URL)
	PlaybackScheme  string // default "https"
}

// ErrVodCredentialsMissing is returned when the channel does not have VOD
// credentials configured. The caller should map this to a 400 with a clear
// "请在 newapi 后台为该 channel 配置 vod_ak/vod_sk/vod_space" message.
var ErrVodCredentialsMissing = errors.New("vod credentials not configured on channel (missing vod_ak / vod_sk / vod_space in OtherInfo)")

// LoadVodCredentialsFromChannel pulls VOD-specific config out of the channel's
// OtherInfo map. Tries snake_case first, then camelCase, so admins can use
// whichever the UI exposes.
//
// `otherInfo` is typically `channel.GetOtherInfo()` from the model layer.
// Pass nil → returns ErrVodCredentialsMissing.
func LoadVodCredentialsFromChannel(otherInfo map[string]interface{}) (*VodCredentials, error) {
	if otherInfo == nil {
		return nil, ErrVodCredentialsMissing
	}

	ak := pickString(otherInfo, "vod_ak", "vodAccessKey", "vod_access_key", "vodAk")
	sk := pickString(otherInfo, "vod_sk", "vodSecretKey", "vod_secret_key", "vodSk")
	space := pickString(otherInfo, "vod_space", "vodSpace", "vod_space_name", "vodSpaceName")
	region := pickString(otherInfo, "vod_region", "vodRegion")
	if region == "" {
		region = "cn-north-1"
	}
	domain := pickString(otherInfo, "vod_playback_domain", "vodPlaybackDomain")
	scheme := pickString(otherInfo, "vod_playback_scheme", "vodPlaybackScheme")
	if scheme != "https" && scheme != "http" {
		scheme = "https"
	}

	if ak == "" || sk == "" || space == "" {
		return nil, fmt.Errorf("%w (have ak=%t sk=%t space=%t)", ErrVodCredentialsMissing, ak != "", sk != "", space != "")
	}
	return &VodCredentials{
		AccessKey:      ak,
		SecretKey:      sk,
		Space:          space,
		Region:         region,
		PlaybackDomain: domain,
		PlaybackScheme: scheme,
	}, nil
}

// LoadVodCredentialsFromRelayInfo is a convenience wrapper that pulls the
// channel-id out of RelayInfo, fetches the channel, and parses its OtherInfo.
// Callers that already have an OtherInfo map should use
// LoadVodCredentialsFromChannel directly.
//
// We accept the `getOtherInfo` callback rather than importing model.Channel
// here to keep this package free of DB / model dependencies (which would
// inflate test fixtures). Real callers pass:
//
//	func(channelID int) (map[string]interface{}, error) {
//	    ch, err := model.GetChannelById(channelID, false)
//	    if err != nil { return nil, err }
//	    return ch.GetOtherInfo(), nil
//	}
func LoadVodCredentialsFromRelayInfo(info *relaycommon.RelayInfo, getOtherInfo func(channelID int) (map[string]interface{}, error)) (*VodCredentials, error) {
	if info == nil || info.ChannelMeta == nil {
		return nil, fmt.Errorf("LoadVodCredentialsFromRelayInfo: nil relay info / channel meta")
	}
	if getOtherInfo == nil {
		return nil, fmt.Errorf("LoadVodCredentialsFromRelayInfo: getOtherInfo callback is required")
	}
	info_, err := getOtherInfo(info.ChannelId)
	if err != nil {
		return nil, fmt.Errorf("load channel other_info: %w", err)
	}
	return LoadVodCredentialsFromChannel(info_)
}

func pickString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}
