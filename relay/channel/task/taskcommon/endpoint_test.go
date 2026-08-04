package taskcommon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEndpointURL(t *testing.T) {
	tests := []struct {
		name         string
		baseURL      string
		pathTemplate string
		variables    map[string]string
		want         string
		wantError    string
	}{
		{
			name:         "submit path",
			baseURL:      "https://relay.example",
			pathTemplate: "/v1/submit?region=cn",
			want:         "https://relay.example/v1/submit?region=cn",
		},
		{
			name:         "base path prefix",
			baseURL:      "https://relay.example/vendor/",
			pathTemplate: "/v1/tasks/{task_id}",
			variables:    map[string]string{"task_id": "task-123"},
			want:         "https://relay.example/vendor/v1/tasks/task-123",
		},
		{
			name:         "escaped task ID in path",
			baseURL:      "https://relay.example/vendor",
			pathTemplate: "/v1/tasks/{task_id}?detail=true",
			variables:    map[string]string{"task_id": "folder/task 1"},
			want:         "https://relay.example/vendor/v1/tasks/folder%2Ftask%201?detail=true",
		},
		{
			name:         "escaped task ID in query",
			baseURL:      "https://relay.example/vendor",
			pathTemplate: "/v1/tasks?task_id={task_id}",
			variables:    map[string]string{"task_id": "task/1 + next"},
			want:         "https://relay.example/vendor/v1/tasks?task_id=task%2F1+%2B+next",
		},
		{
			name:         "cross origin template",
			baseURL:      "https://relay.example",
			pathTemplate: "//evil.example/v1/tasks/{task_id}",
			variables:    map[string]string{"task_id": "task-1"},
			wantError:    "exactly one /",
		},
		{
			name:         "malformed template",
			baseURL:      "https://relay.example",
			pathTemplate: "/v1/%zz/tasks/{task_id}",
			variables:    map[string]string{"task_id": "task-1"},
			wantError:    "valid URL path",
		},
		{
			name:         "malformed query escape",
			baseURL:      "https://relay.example",
			pathTemplate: "/v1/tasks/{task_id}?token=%zz",
			variables:    map[string]string{"task_id": "task-1"},
			wantError:    "malformed URL escapes",
		},
		{
			name:         "unknown variable",
			baseURL:      "https://relay.example",
			pathTemplate: "/v1/submit",
			variables:    map[string]string{"operation_id": "operation-1"},
			wantError:    "unsupported endpoint variable",
		},
		{
			name:         "relative base URL",
			baseURL:      "/relay",
			pathTemplate: "/v1/submit",
			wantError:    "absolute http or https URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveEndpointURL(tt.baseURL, tt.pathTemplate, tt.variables)
			if tt.wantError == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}
