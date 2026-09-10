package minimax

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMiniMaxFilesUploadURLPreservesBasePath(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "metaso prefix",
			baseURL: "https://metaso.cn/api/minimax",
			want:    "https://metaso.cn/api/minimax/v1/files/upload",
		},
		{
			name:    "trailing slash",
			baseURL: "https://metaso.cn/api/minimax/",
			want:    "https://metaso.cn/api/minimax/v1/files/upload",
		},
		{
			name: "default base URL",
			want: "https://api.minimax.chat/v1/files/upload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildMiniMaxFilesUploadURL(tt.baseURL)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMiniMaxFileResponseNormalizesFileIDToString(t *testing.T) {
	for _, body := range []string{
		`{"file":{"file_id":424010985738629,"filename":"clip.mp4"},"base_resp":{"status_code":0}}`,
		`{"file":{"file_id":"424010985738629","filename":"clip.mp4"},"base_resp":{"status_code":0}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var response MiniMaxFileResponse
			require.NoError(t, json.Unmarshal([]byte(body), &response))
			assert.Equal(t, "424010985738629", response.File.FileID)

			encoded, err := json.Marshal(response)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"file_id":"424010985738629"`)
		})
	}
}
