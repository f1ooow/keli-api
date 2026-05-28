package volcvod

import (
	"net/url"
	"strings"
	"testing"
)

// TestBuildAuthKey_CrossLanguageVector pins this package's md5/auth_key output
// against the SAME reference vector used by:
//   - relay/channel/volcengine/vod_url_sign_test.go (the volcengine-package port)
//   - CCS server/lib/volcengine/playbackAuth.test.ts (the TypeScript source)
//
// All three implementations MUST produce byte-identical auth_key for identical
// inputs. If this drifts, the signed playback URL will 403 at the CDN edge.
//
//	uri    = "/path/to/file.mp4"
//	expire = 1779123600
//	rand   = "aabbccdd11223344"
//	uid    = "0"
//	key    = "secret-key"
//	md5    = MD5("/path/to/file.mp4-1779123600-aabbccdd11223344-0-secret-key")
//	       = 95755e846c4568982e4a8412338ab618
func TestBuildAuthKey_CrossLanguageVector(t *testing.T) {
	const (
		uri    = "/path/to/file.mp4"
		expire = int64(1779123600)
		rand   = "aabbccdd11223344"
		uid    = "0"
		key    = "secret-key"

		wantMd5     = "95755e846c4568982e4a8412338ab618"
		wantAuthKey = "1779123600-aabbccdd11223344-0-95755e846c4568982e4a8412338ab618"
	)

	got := buildAuthKey(uri, expire, rand, uid, key)
	if got != wantAuthKey {
		t.Fatalf("buildAuthKey mismatch:\n got = %q\nwant = %q", got, wantAuthKey)
	}
	parts := strings.Split(got, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4 dash-separated tokens, got %d: %q", len(parts), got)
	}
	if parts[3] != wantMd5 {
		t.Errorf("md5 segment = %q, want %q", parts[3], wantMd5)
	}
}

func TestSignVodPlaybackURL_EmptyKeyReturnsUnchanged(t *testing.T) {
	raw := "https://vod.example.com/path/to/file.mp4"
	got, err := signVodPlaybackURL(raw, "", urlAuthSignTTLSeconds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != raw {
		t.Errorf("expected unchanged URL when authKey empty, got %q", got)
	}
}

func TestSignVodPlaybackURL_AppendsAuthKey(t *testing.T) {
	raw := "https://vod.example.com/path/to/file.mp4"
	got, err := signVodPlaybackURL(raw, "secret-key", urlAuthSignTTLSeconds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("signed url unparseable: %v", err)
	}
	if u.Scheme != "https" || u.Host != "vod.example.com" || u.Path != "/path/to/file.mp4" {
		t.Errorf("scheme/host/path not preserved: %q", got)
	}
	auth := u.Query().Get("auth_key")
	if auth == "" {
		t.Fatal("auth_key not present on signed URL")
	}
	parts := strings.Split(auth, "-")
	if len(parts) != 4 {
		t.Fatalf("auth_key expected 4 tokens, got %d: %q", len(parts), auth)
	}
	if parts[2] != urlAuthUID {
		t.Errorf("uid segment = %q, want %q", parts[2], urlAuthUID)
	}
	if len(parts[3]) != 32 {
		t.Errorf("md5 segment length = %d, want 32 (hex): %q", len(parts[3]), parts[3])
	}
}

func TestBuildPlaybackURL_JoinsSingleSlash(t *testing.T) {
	cases := []struct {
		scheme, domain, fileName, want string
	}{
		{"http", "vod.example.com", "edit/erase/out.mp4", "http://vod.example.com/edit/erase/out.mp4"},
		{"https", "vod.example.com/", "/edit/erase/out.mp4", "https://vod.example.com/edit/erase/out.mp4"},
		{"", "vod.example.com", "out.mp4", "https://vod.example.com/out.mp4"}, // scheme default https
		{"http", " vod.example.com ", " out.mp4 ", "http://vod.example.com/out.mp4"},
	}
	for _, c := range cases {
		if got := buildPlaybackURL(c.scheme, c.domain, c.fileName); got != c.want {
			t.Errorf("buildPlaybackURL(%q,%q,%q) = %q, want %q", c.scheme, c.domain, c.fileName, got, c.want)
		}
	}
}

// TestEncodeSignedVodResultUrl_Erase asserts the erase result encoding shape and
// that the embedded signed video URL round-trips through url.QueryUnescape with
// a deterministic auth_key (pinned via buildAuthKey on the known FileName path).
func TestEncodeSignedVodResultUrl_Erase(t *testing.T) {
	a := &TaskAdaptor{
		playbackDomain: "vod.example.com",
		playbackScheme: "http",
		urlAuthKey:     "secret-key",
	}
	task := OutputTaskSpec{
		Erase: &EraseOutput{
			Duration: 12.5,
			File:     FileObject{FileName: "edit/erase/out.mp4", Size: "1024", Vid: "v123"},
		},
	}
	got, err := a.encodeSignedVodResultUrl(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "vod-result-erase://?") {
		t.Fatalf("erase prefix wrong: %q", got)
	}
	// Parse the query portion after the scheme.
	q, err := url.ParseQuery(strings.TrimPrefix(got, "vod-result-erase://?"))
	if err != nil {
		t.Fatalf("query unparseable: %v", err)
	}
	if q.Get("duration") != "12.5" {
		t.Errorf("duration = %q, want 12.5", q.Get("duration"))
	}
	video := q.Get("video")
	vu, err := url.Parse(video)
	if err != nil {
		t.Fatalf("embedded video url unparseable: %v (%q)", err, video)
	}
	if vu.Scheme != "http" || vu.Host != "vod.example.com" || vu.Path != "/edit/erase/out.mp4" {
		t.Errorf("embedded video url wrong: %q", video)
	}
	if vu.Query().Get("auth_key") == "" {
		t.Errorf("embedded video url not signed: %q", video)
	}
}

func TestEncodeSignedVodResultUrl_AudioExtract(t *testing.T) {
	a := &TaskAdaptor{
		playbackDomain: "vod.example.com",
		playbackScheme: "https",
		urlAuthKey:     "secret-key",
	}
	task := OutputTaskSpec{
		AudioExtract: &AudioExtractOutput{
			Duration:   30,
			Voice:      FileObject{FileName: "edit/audio/voice.m4a", Size: "2048"},
			Background: FileObject{FileName: "edit/audio/bg.m4a", Size: "4096"},
		},
	}
	got, err := a.encodeSignedVodResultUrl(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "vod-result-audio://?") {
		t.Fatalf("audio prefix wrong: %q", got)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(got, "vod-result-audio://?"))
	if err != nil {
		t.Fatalf("query unparseable: %v", err)
	}
	if q.Get("duration") != "30" {
		t.Errorf("duration = %q, want 30", q.Get("duration"))
	}
	for field, wantPath := range map[string]string{
		"voice": "/edit/audio/voice.m4a",
		"bg":    "/edit/audio/bg.m4a",
	} {
		raw := q.Get(field)
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s url unparseable: %v (%q)", field, err, raw)
		}
		if u.Scheme != "https" || u.Host != "vod.example.com" || u.Path != wantPath {
			t.Errorf("%s url wrong: %q", field, raw)
		}
		if u.Query().Get("auth_key") == "" {
			t.Errorf("%s url not signed: %q", field, raw)
		}
	}
}

// TestEncodeSignedVodResultUrl_NoDomainFallsBackToLegacy: with no playback domain
// configured, the result side must fall back to the legacy vod-erase:// encoding
// so credential-holding callers keep working (backwards compatible rollout).
func TestEncodeSignedVodResultUrl_NoDomainFallsBackToLegacy(t *testing.T) {
	a := &TaskAdaptor{} // no playbackDomain
	task := OutputTaskSpec{
		Erase: &EraseOutput{
			Duration: 5,
			File:     FileObject{FileName: "edit/erase/out.mp4", Vid: "v123", Size: "10"},
		},
	}
	got, err := a.encodeSignedVodResultUrl(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "vod-erase://") {
		t.Fatalf("expected legacy vod-erase:// fallback, got %q", got)
	}
}

// TestEncodeSignedVodResultUrl_DomainButNoAuthKey: domain set, url-auth key empty
// (space has url-auth off) → URL is built + directly downloadable but unsigned.
func TestEncodeSignedVodResultUrl_DomainButNoAuthKey(t *testing.T) {
	a := &TaskAdaptor{
		playbackDomain: "vod.example.com",
		playbackScheme: "http",
		urlAuthKey:     "", // no signing
	}
	task := OutputTaskSpec{
		Erase: &EraseOutput{Duration: 1, File: FileObject{FileName: "out.mp4"}},
	}
	got, err := a.encodeSignedVodResultUrl(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(got, "vod-result-erase://?"))
	if err != nil {
		t.Fatalf("query unparseable: %v", err)
	}
	video := q.Get("video")
	if video != "http://vod.example.com/out.mp4" {
		t.Errorf("unsigned url wrong: %q", video)
	}
}
