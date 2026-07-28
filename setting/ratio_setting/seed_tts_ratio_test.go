package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSeedTTSV3UsesCharacterBillingRatio(t *testing.T) {
	assert.Equal(t, 250.0, defaultModelRatio["seed-tts-2.0-standard"])
	assert.Equal(t, 250.0, defaultModelRatio["doubao-tts-2.0"])
}
