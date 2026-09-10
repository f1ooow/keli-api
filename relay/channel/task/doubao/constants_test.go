package doubao

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedanceModelListIncludesCurrentSeries(t *testing.T) {
	require.Contains(t, ModelList, "doubao-seedance-2-5-260628")
	require.Contains(t, ModelList, "doubao-seedance-2-0-260128")
	require.Contains(t, ModelList, "doubao-seedance-2-0-fast-260128")
	require.Contains(t, ModelList, "doubao-seedance-2-0-mini-260615")
}

func TestGetVideoInputRatioUsesOfficialVideoInputPrices(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		resolution string
		hasVideo   bool
		want       float64
	}{
		{
			name:       "seedance 2.5 video input",
			model:      "doubao-seedance-2-5-260628",
			resolution: "720p",
			hasVideo:   true,
			want:       42.0 / 70.0,
		},
		{
			name:       "seedance mini promotional video input",
			model:      "doubao-seedance-2-0-mini-260615",
			resolution: "480p",
			hasVideo:   true,
			want:       5.6 / 9.2,
		},
		{
			name:       "seedance fast promotional video input",
			model:      "doubao-seedance-2-0-fast-260128",
			resolution: "720p",
			hasVideo:   true,
			want:       16.5 / 27.75,
		},
		{
			name:       "seedance base 1080p without video",
			model:      "doubao-seedance-2-0-260128",
			resolution: "1080p",
			hasVideo:   false,
			want:       51.0 / 46.0,
		},
		{
			name:       "seedance base 4k with video",
			model:      "doubao-seedance-2-0-260128",
			resolution: "4k",
			hasVideo:   true,
			want:       16.0 / 46.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GetVideoInputRatio(tt.model, tt.resolution, tt.hasVideo)
			require.True(t, ok)
			assert.InDelta(t, tt.want, got, 1e-9)
		})
	}
}

func TestGetVideoInputRatioUsesNoVideoBase(t *testing.T) {
	for _, modelName := range []string{
		"doubao-seedance-2-5-260628",
		"doubao-seedance-2-0-260128",
		"doubao-seedance-2-0-fast-260128",
		"doubao-seedance-2-0-mini-260615",
	} {
		ratio, ok := GetVideoInputRatio(modelName, "720p", false)
		require.True(t, ok)
		assert.Equal(t, 1.0, ratio)
	}
}
