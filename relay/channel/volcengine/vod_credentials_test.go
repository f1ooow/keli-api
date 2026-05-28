package volcengine

import (
	"errors"
	"testing"
)

func TestLoadVodCredentialsFromChannel_HappyPath(t *testing.T) {
	creds, err := LoadVodCredentialsFromChannel(map[string]interface{}{
		"vod_ak":              "AKLT-xxx",
		"vod_sk":              "secret-xxx",
		"vod_space":           "kc",
		"vod_region":          "cn-shanghai",
		"vod_playback_domain": "vod.example.com",
		"vod_playback_scheme": "https",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.AccessKey != "AKLT-xxx" || creds.SecretKey != "secret-xxx" || creds.Space != "kc" {
		t.Errorf("ak/sk/space mismatch: %+v", creds)
	}
	if creds.Region != "cn-shanghai" {
		t.Errorf("region = %q", creds.Region)
	}
	if creds.PlaybackDomain != "vod.example.com" {
		t.Errorf("PlaybackDomain = %q", creds.PlaybackDomain)
	}
	if creds.PlaybackScheme != "https" {
		t.Errorf("PlaybackScheme = %q", creds.PlaybackScheme)
	}
}

func TestLoadVodCredentialsFromChannel_CamelCase(t *testing.T) {
	creds, err := LoadVodCredentialsFromChannel(map[string]interface{}{
		"vodAccessKey": "AKLT-y",
		"vodSecretKey": "secret-y",
		"vodSpace":     "kc-camel",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.AccessKey != "AKLT-y" {
		t.Errorf("AccessKey = %q", creds.AccessKey)
	}
	if creds.SecretKey != "secret-y" {
		t.Errorf("SecretKey = %q", creds.SecretKey)
	}
	if creds.Space != "kc-camel" {
		t.Errorf("Space = %q", creds.Space)
	}
	if creds.Region != "cn-north-1" {
		t.Errorf("default Region = %q, want cn-north-1", creds.Region)
	}
	if creds.PlaybackScheme != "https" {
		t.Errorf("default PlaybackScheme = %q, want https", creds.PlaybackScheme)
	}
}

func TestLoadVodCredentialsFromChannel_Missing(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]interface{}
	}{
		{"nil map", nil},
		{"empty map", map[string]interface{}{}},
		{"only ak", map[string]interface{}{"vod_ak": "x"}},
		{"ak+sk no space", map[string]interface{}{"vod_ak": "x", "vod_sk": "y"}},
		{"empty string fields", map[string]interface{}{"vod_ak": "", "vod_sk": "", "vod_space": ""}},
		{"wrong type", map[string]interface{}{"vod_ak": 123, "vod_sk": true, "vod_space": []string{"x"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadVodCredentialsFromChannel(c.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrVodCredentialsMissing) {
				t.Errorf("expected ErrVodCredentialsMissing, got %v", err)
			}
		})
	}
}

func TestLoadVodCredentialsFromChannel_HttpScheme(t *testing.T) {
	creds, err := LoadVodCredentialsFromChannel(map[string]interface{}{
		"vod_ak":              "x",
		"vod_sk":              "y",
		"vod_space":           "kc",
		"vod_playback_scheme": "http",
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.PlaybackScheme != "http" {
		t.Errorf("PlaybackScheme = %q, want http", creds.PlaybackScheme)
	}
}

func TestLoadVodCredentialsFromChannel_InvalidScheme(t *testing.T) {
	// Invalid scheme should fall back to "https".
	creds, err := LoadVodCredentialsFromChannel(map[string]interface{}{
		"vod_ak":              "x",
		"vod_sk":              "y",
		"vod_space":           "kc",
		"vod_playback_scheme": "ftp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.PlaybackScheme != "https" {
		t.Errorf("PlaybackScheme fallback = %q, want https", creds.PlaybackScheme)
	}
}
