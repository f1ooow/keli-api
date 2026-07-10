package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVideoTaskAdaptor 模拟视频任务适配器：FetchTask 返回固定原始响应体，
// ParseTaskResult 返回固定 TaskInfo（对应 ali adaptor 对 DashScope 状态的映射结果）。
type fakeVideoTaskAdaptor struct {
	rawBody  string
	taskInfo *relaycommon.TaskInfo
}

func (f *fakeVideoTaskAdaptor) Init(info *relaycommon.RelayInfo) {}

func (f *fakeVideoTaskAdaptor) FetchTask(baseURL string, key string, body map[string]any, proxy string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(f.rawBody)),
	}, nil
}

func (f *fakeVideoTaskAdaptor) ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	info := *f.taskInfo
	return &info, nil
}

func (f *fakeVideoTaskAdaptor) AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int {
	return 0
}

// DashScope 原生响应 shape（无 "code" 字段，不会被误认成 new-api TaskResponse 透传格式）
const aliUnknownRawBody = `{"request_id":"req-unknown","output":{"task_id":"up_task_1","task_status":"UNKNOWN"}}`

func seedPendingVideoTask(t *testing.T, submitAgeSeconds int64, quota int) (*model.Task, map[string]*model.Task) {
	t.Helper()
	task := makeTask(1, 1, quota, 1, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusQueued)
	task.Progress = "20%"
	task.SubmitTime = time.Now().Unix() - submitAgeSeconds
	task.PrivateData.UpstreamTaskID = "up_task_1"
	require.NoError(t, model.DB.Create(task).Error)
	return task, map[string]*model.Task{"up_task_1": task}
}

// ===========================================================================
// UNKNOWN 状态宽限（修复：DashScope 早期 UNKNOWN 曾被立即判死 + 误退款）
// ===========================================================================

func TestUpdateVideoSingleTask_UnknownWithinGraceKeepsTaskAlive(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const initQuota, preConsumed = 10000, 3000
	seedUser(t, 1, initQuota)
	seedToken(t, 1, 1, "sk-test-key", 5000)
	seedChannel(t, 1)
	task, taskM := seedPendingVideoTask(t, 10, preConsumed) // 提交 10s，宽限期内

	adaptor := &fakeVideoTaskAdaptor{
		rawBody:  aliUnknownRawBody,
		taskInfo: &relaycommon.TaskInfo{Status: model.TaskStatusUnknown},
	}

	err := updateVideoSingleTask(ctx, adaptor, &model.Channel{Id: 1, Key: "sk-test"}, "up_task_1", taskM)
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	// 任务行不落库改动：保持非终态留在刷新集合，下一轮继续查
	assert.EqualValues(t, model.TaskStatusQueued, reloaded.Status)
	assert.Equal(t, "20%", reloaded.Progress)
	assert.Empty(t, reloaded.FailReason)
	// 宽限期内绝不退款
	assert.Equal(t, initQuota, getUserQuota(t, 1))
}

func TestUpdateVideoSingleTask_UnknownBeyondGraceFailsAndRefunds(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const initQuota, preConsumed = 10000, 3000
	seedUser(t, 1, initQuota)
	seedToken(t, 1, 1, "sk-test-key", 5000)
	seedChannel(t, 1)
	task, taskM := seedPendingVideoTask(t, model.TaskUnknownStatusGraceSeconds+60, preConsumed) // 超窗

	adaptor := &fakeVideoTaskAdaptor{
		rawBody:  aliUnknownRawBody,
		taskInfo: &relaycommon.TaskInfo{Status: model.TaskStatusUnknown},
	}

	err := updateVideoSingleTask(ctx, adaptor, &model.Channel{Id: 1, Key: "sk-test"}, "up_task_1", taskM)
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	// 有界判死：超窗仍 UNKNOWN → FAILURE 终态 + 退款
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Equal(t, "100%", reloaded.Progress)
	assert.NotZero(t, reloaded.FinishTime)
	assert.Contains(t, reloaded.FailReason, "UNKNOWN")
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, 1))
}

func TestUpdateVideoSingleTask_FailedStatusStillFailsImmediately(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const initQuota, preConsumed = 10000, 3000
	seedUser(t, 1, initQuota)
	seedToken(t, 1, 1, "sk-test-key", 5000)
	seedChannel(t, 1)
	task, taskM := seedPendingVideoTask(t, 10, preConsumed) // 刚提交

	adaptor := &fakeVideoTaskAdaptor{
		rawBody:  `{"request_id":"req-failed","message":"boom","output":{"task_id":"up_task_1","task_status":"FAILED"}}`,
		taskInfo: &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "boom"},
	}

	err := updateVideoSingleTask(ctx, adaptor, &model.Channel{Id: 1, Key: "sk-test"}, "up_task_1", taskM)
	require.NoError(t, err)

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	// 明确 FAILED 不吃宽限：行为与修复前完全一致（立即终态 + 退款）
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Equal(t, "100%", reloaded.Progress)
	assert.Equal(t, "boom", reloaded.FailReason)
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, 1))
}
