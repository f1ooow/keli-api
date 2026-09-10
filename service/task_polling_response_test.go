package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestIsMeaningfulVideoTaskResponse(t *testing.T) {
	tests := []struct {
		name     string
		response dto.TaskResponse[model.Task]
		want     bool
	}{
		{
			name: "recognized task status",
			response: dto.TaskResponse[model.Task]{
				Code: dto.TaskSuccessCode,
				Data: model.Task{TaskID: "task_public", Status: model.TaskStatusInProgress},
			},
			want: true,
		},
		{
			name: "success code with empty data falls through",
			response: dto.TaskResponse[model.Task]{
				Code: dto.TaskSuccessCode,
			},
			want: false,
		},
		{
			name: "provider status is not an internal passthrough status",
			response: dto.TaskResponse[model.Task]{
				Code: dto.TaskSuccessCode,
				Data: model.Task{Status: model.TaskStatus("running")},
			},
			want: false,
		},
		{
			name: "failed envelope never bypasses adaptor",
			response: dto.TaskResponse[model.Task]{
				Code: "error",
				Data: model.Task{Status: model.TaskStatusFailure},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsMeaningfulVideoTaskResponse(&tt.response))
		})
	}
}
