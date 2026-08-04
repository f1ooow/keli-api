package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelValidateSettingsValidatesTaskEndpointOverride(t *testing.T) {
	tests := []struct {
		name          string
		otherSettings string
		wantError     string
	}{
		{name: "legacy empty settings"},
		{name: "valid override", otherSettings: `{"task_endpoint_override":{"submit_path":"/v1/submit","fetch_path":"/v1/tasks/{task_id}"}}`},
		{name: "invalid override", otherSettings: `{"task_endpoint_override":{"fetch_path":"https://evil.example/tasks/{task_id}"}}`, wantError: "task_endpoint_override.fetch_path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{OtherSettings: tt.otherSettings}
			err := channel.ValidateSettings()
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}
