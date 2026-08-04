package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvancedCustomValidateResponsesToChatConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
			},
		},
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name         string
		incomingPath string
	}{
		{name: "chat completions", incomingPath: "/v1/chat/completions"},
		{name: "responses compact", incomingPath: "/v1/responses/compact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: tt.incomingPath,
						UpstreamPath: "/v1/chat/completions",
						Converter:    AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestTaskEndpointOverrideValidate(t *testing.T) {
	tests := []struct {
		name      string
		override  *TaskEndpointOverride
		wantError string
	}{
		{name: "nil override"},
		{name: "blank paths", override: &TaskEndpointOverride{}},
		{
			name: "valid paths",
			override: &TaskEndpointOverride{
				SubmitPath: "/v1/services/aigc/video-generation/video-synthesis?region=cn",
				FetchPath:  "/v1/tasks/{task_id}?detail=true",
			},
		},
		{
			name:      "submit absolute URL",
			override:  &TaskEndpointOverride{SubmitPath: "https://evil.example/submit"},
			wantError: "submit_path: must begin with exactly one /",
		},
		{
			name:      "submit network path",
			override:  &TaskEndpointOverride{SubmitPath: "//evil.example/submit"},
			wantError: "submit_path: must begin with exactly one /",
		},
		{
			name:      "submit placeholder",
			override:  &TaskEndpointOverride{SubmitPath: "/submit/{task_id}"},
			wantError: "submit_path: must not contain placeholders",
		},
		{
			name:      "fetch missing task ID",
			override:  &TaskEndpointOverride{FetchPath: "/v1/tasks"},
			wantError: "fetch_path: must contain {task_id} exactly once",
		},
		{
			name:      "fetch duplicate task ID",
			override:  &TaskEndpointOverride{FetchPath: "/v1/tasks/{task_id}/{task_id}"},
			wantError: "fetch_path: must contain {task_id} exactly once",
		},
		{
			name:      "unknown placeholder",
			override:  &TaskEndpointOverride{FetchPath: "/v1/tasks/{operation_id}"},
			wantError: "fetch_path: contains an unsupported placeholder",
		},
		{
			name:      "fragment",
			override:  &TaskEndpointOverride{SubmitPath: "/v1/submit#upstream"},
			wantError: "submit_path: must not contain a fragment",
		},
		{
			name:      "dot segment",
			override:  &TaskEndpointOverride{SubmitPath: "/v1/../submit"},
			wantError: "submit_path: must not contain . or .. path segments",
		},
		{
			name:      "encoded dot segment",
			override:  &TaskEndpointOverride{SubmitPath: "/v1/%2e%2e/submit"},
			wantError: "submit_path: must not contain . or .. path segments",
		},
		{
			name:      "malformed escape",
			override:  &TaskEndpointOverride{SubmitPath: "/v1/%zz/submit"},
			wantError: "submit_path: must be a valid URL path",
		},
		{
			name:      "malformed query escape",
			override:  &TaskEndpointOverride{SubmitPath: "/v1/submit?token=%zz"},
			wantError: "submit_path: must not contain malformed URL escapes",
		},
		{
			name:      "surrounding whitespace",
			override:  &TaskEndpointOverride{SubmitPath: " /v1/submit"},
			wantError: "submit_path: must not contain leading or trailing whitespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.override.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}
