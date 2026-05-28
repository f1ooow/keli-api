package volcengine

import (
	"net/url"
	"strings"
	"testing"
)

// TestBuildAuthKey_CrossLanguageVector pins the Go md5/auth_key output against
// the CCS TypeScript reference implementation
// (server/lib/volcengine/playbackAuth.test.ts).
//
// The TS test signs with:
//
//	uri    = "/path/to/file.mp4"
//	expire = Math.floor(Date.UTC(2026, 4, 18, 16, 0, 0) / 1000) + 3600
//	       = 1779120000 + 3600 = 1779123600
//	rand   = "aabbccdd11223344"
//	uid    = "0"
//	key    = "secret-key"
//	md5    = MD5("/path/to/file.mp4-1779123600-aabbccdd11223344-0-secret-key")
//
// Cross-checked with three independent implementations (all agree):
//
//	node -e 'console.log(require("crypto").createHash("md5").update(
//	    "/path/to/file.mp4-1779123600-aabbccdd11223344-0-secret-key").digest("hex"))'
//	  → 95755e846c4568982e4a8412338ab618
//
//	python3 -c 'import hashlib; print(hashlib.md5(
//	    b"/path/to/file.mp4-1779123600-aabbccdd11223344-0-secret-key").hexdigest())'
//	  → 95755e846c4568982e4a8412338ab618
//
// This test asserts the Go port produces the byte-identical auth_key, proving
// the TS → Go port is correct.
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

	// Also assert the md5 segment in isolation so a future format change is
	// caught precisely (auth_key = "<expire>-<rand>-<uid>-<md5>").
	parts := strings.Split(got, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4 dash-separated tokens, got %d: %q", len(parts), got)
	}
	if parts[3] != wantMd5 {
		t.Errorf("md5 segment = %q, want %q", parts[3], wantMd5)
	}
}

// TestSignVodPlaybackURL_EmptyKeyReturnsUnchanged: when no url-auth key is
// configured (space has url-auth off), the URL is returned verbatim.
func TestSignVodPlaybackURL_EmptyKey(t *testing.T) {
	raw := "https://vod.example.com/path/to/file.mp4"
	got, err := signVodPlaybackURL(raw, "", 3600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != raw {
		t.Errorf("expected unchanged URL, got %q", got)
	}
}

// TestSignVodPlaybackURL_AppendsAuthKey: a non-empty key produces a signed URL
// that (a) preserves scheme/host/path, (b) carries an auth_key with the
// 4-token shape, and (c) signs over the URL path only.
func TestSignVodPlaybackURL_AppendsAuthKey(t *testing.T) {
	raw := "https://vod.example.com/path/to/file.mp4"
	got, err := signVodPlaybackURL(raw, "secret-key", 3600)
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

// TestSignVodPlaybackURL_PreservesExistingQuery: an existing query string is
// retained alongside auth_key.
func TestSignVodPlaybackURL_PreservesExistingQuery(t *testing.T) {
	raw := "https://vod.example.com/path/file.mp4?foo=bar"
	got, err := signVodPlaybackURL(raw, "k", 0) // ttl<=0 → default 3600
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("signed url unparseable: %v", err)
	}
	if u.Query().Get("foo") != "bar" {
		t.Errorf("existing query param dropped: %q", got)
	}
	if u.Query().Get("auth_key") == "" {
		t.Error("auth_key missing")
	}
}
