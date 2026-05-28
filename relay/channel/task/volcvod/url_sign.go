// Volcengine VOD/CDN URL-auth (Type A) signing — self-contained copy.
//
// Reference: https://www.volcengine.com/docs/4/177191
//
// This is a verbatim port of the two small functions in
// relay/channel/volcengine/vod_url_sign.go (buildAuthKey / signVodPlaybackURL).
// We keep an independent copy here (rather than importing the volcengine
// package) to avoid cross-package coupling between the task adaptor layer and
// the (older, ASR-oriented) volcengine relay package.
//
// IMPORTANT: the algorithm MUST stay byte-identical to the volcengine version
// and to the CCS TypeScript implementation
// (server/lib/volcengine/playbackAuth.ts). If you change one, change all three.
// The cross-language reference vector lives in url_sign_test.go here AND in
// relay/channel/volcengine/vod_url_sign_test.go — both must agree.
//
// Format:
//
//	auth_key = "<expire>-<rand>-<uid>-<md5>"
//	md5      = MD5("<uri>-<expire>-<rand>-<uid>-<key>")   // lowercase hex
//
// Where:
//   - <uri>    = URL path (no host, no query string); MUST start with "/"
//   - <expire> = expiry as a unix-second integer
//   - <rand>   = nonce string (16 hex chars from 8 random bytes)
//   - <uid>    = arbitrary "user id"; VOD docs accept "0" for non-user CDN
//   - <key>    = the URL-auth key configured on the playback domain
package volcvod

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// urlAuthDefaultTTLSeconds is applied when the caller passes ttlSeconds <= 0.
	urlAuthDefaultTTLSeconds = 3600
	// urlAuthUID is the fixed UID slot in the auth_key tuple. VOD docs accept
	// "0" for non-user CDN access. Mirrors the TS / volcengine-package default.
	urlAuthUID = "0"
)

// buildAuthKey computes the Volcengine Type-A auth_key tuple for the given
// inputs. Deterministic — tests pin a fixed expire/rand/uid/key and assert the
// exact output against the cross-language reference vector.
//
//	auth_key = "<expire>-<rand>-<uid>-<md5>"
//	md5      = MD5("<uri>-<expire>-<rand>-<uid>-<key>")
func buildAuthKey(uri string, expire int64, rand, uid, key string) string {
	signInput := fmt.Sprintf("%s-%d-%s-%s-%s", uri, expire, rand, uid, key)
	sum := md5.Sum([]byte(signInput))
	digest := hex.EncodeToString(sum[:])
	return fmt.Sprintf("%d-%s-%s-%s", expire, rand, uid, digest)
}

// signVodPlaybackURL appends a Volcengine Type-A auth_key to rawURL.
// Returns rawURL unchanged if authKey is empty (the space has url-auth off).
//
// ttlSeconds <= 0 falls back to urlAuthDefaultTTLSeconds (3600s). The rand
// nonce is 16 hex chars from 8 crypto/rand bytes; uid is fixed to "0".
func signVodPlaybackURL(rawURL, authKey string, ttlSeconds int) (string, error) {
	if authKey == "" {
		return rawURL, nil
	}
	if ttlSeconds <= 0 {
		ttlSeconds = urlAuthDefaultTTLSeconds
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("signVodPlaybackURL: parse url: %w", err)
	}

	uri := u.Path
	if uri == "" {
		uri = "/"
	}

	nonce, err := randomHexNonce()
	if err != nil {
		return "", fmt.Errorf("signVodPlaybackURL: generate nonce: %w", err)
	}

	expire := time.Now().Unix() + int64(ttlSeconds)
	authValue := buildAuthKey(uri, expire, nonce, urlAuthUID, authKey)

	q := u.Query()
	q.Set("auth_key", authValue)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// randomHexNonce returns 16 lowercase hex chars (8 random bytes), matching the
// TS `randomBytes(8).toString('hex')`.
func randomHexNonce() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// buildPlaybackURL assembles the unsigned playback URL from the channel's
// playback domain + scheme and the VOD product FileName (the "加速域名拼接路径").
//
//	scheme://domain/<FileName>
//
// FileName from the VOD API already carries any leading path; we normalize the
// single slash join so we never produce "domain//path" or "domainpath".
func buildPlaybackURL(scheme, domain, fileName string) string {
	if scheme == "" {
		scheme = "https"
	}
	domain = strings.Trim(strings.TrimSpace(domain), "/")
	fileName = strings.TrimPrefix(strings.TrimSpace(fileName), "/")
	return fmt.Sprintf("%s://%s/%s", scheme, domain, fileName)
}
