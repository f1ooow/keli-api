package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// InUnknownStatusGrace 决定上游 UNKNOWN 状态的任务是宽限存活还是有界判死
// （消费方：service/task_polling.go updateVideoSingleTask）。

func TestInUnknownStatusGrace_WithinWindow(t *testing.T) {
	now := time.Now().Unix()
	task := &Task{SubmitTime: now - 30}
	assert.True(t, task.InUnknownStatusGrace(now), "刚提交 30s 的任务必须存活（DashScope 早期 UNKNOWN 不可判死）")
}

func TestInUnknownStatusGrace_AtBoundary(t *testing.T) {
	now := time.Now().Unix()
	task := &Task{SubmitTime: now - TaskUnknownStatusGraceSeconds}
	assert.True(t, task.InUnknownStatusGrace(now), "恰好等于宽限窗口仍算存活（<=）")
}

func TestInUnknownStatusGrace_BeyondWindow(t *testing.T) {
	now := time.Now().Unix()
	task := &Task{SubmitTime: now - TaskUnknownStatusGraceSeconds - 1}
	assert.False(t, task.InUnknownStatusGrace(now), "超窗任务必须可判死，不许永久 pending")
}

func TestInUnknownStatusGrace_ZeroSubmitTime(t *testing.T) {
	// 异常行（无 SubmitTime）视为已超窗，立即可判死 —— 与旧行为一致
	assert.False(t, (&Task{}).InUnknownStatusGrace(time.Now().Unix()))
}
