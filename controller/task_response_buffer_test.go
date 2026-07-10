package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBufferTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	return c, rec
}

// 核心竞态修复契约：adaptor 在 DoResponse 里写出的含 taskId 的响应，在
// flushToClient（task.Insert() 之后）之前绝不能到达客户端。
func TestTaskResponseBufferHoldsResponseUntilFlush(t *testing.T) {
	c, rec := newBufferTestContext(t)
	buf := newTaskResponseBuffer(c.Writer)
	c.Writer = buf

	c.JSON(http.StatusOK, gin.H{"task_id": "task_abc123"})

	// flush 前：连接上没有任何字节
	assert.Zero(t, rec.Body.Len())
	// 但对 handler 侧语义正确：已产生响应
	assert.True(t, buf.Written())
	assert.Equal(t, http.StatusOK, buf.Status())

	buf.flushToClient()

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "task_abc123")
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
}

func TestTaskResponseBufferResetDiscardsPreviousAttempt(t *testing.T) {
	c, rec := newBufferTestContext(t)
	buf := newTaskResponseBuffer(c.Writer)
	c.Writer = buf

	// 第一次尝试写了脏响应（假想的异常 adaptor），重试前 reset 丢弃
	c.JSON(http.StatusInternalServerError, gin.H{"error": "attempt-1-garbage"})
	buf.reset()
	c.JSON(http.StatusOK, gin.H{"task_id": "attempt-2-ok"})
	buf.flushToClient()

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "attempt-2-ok")
	assert.NotContains(t, rec.Body.String(), "attempt-1-garbage")
}

// 错误路径：循环内没有任何 DoResponse 写入，flush 不产生输出，
// 随后 respondTaskError 直写真实 writer（与修复前行为一致）。
func TestTaskResponseBufferEmptyFlushLeavesWriterUsable(t *testing.T) {
	c, rec := newBufferTestContext(t)
	buf := newTaskResponseBuffer(c.Writer)
	c.Writer = buf

	buf.flushToClient()
	c.Writer = buf.ResponseWriter

	c.JSON(http.StatusBadRequest, gin.H{"code": "fail_to_fetch_task"})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "fail_to_fetch_task")
}

// flush 之后进入直通模式：残留引用继续写不会被吞掉
func TestTaskResponseBufferPassesThroughAfterFlush(t *testing.T) {
	c, rec := newBufferTestContext(t)
	buf := newTaskResponseBuffer(c.Writer)
	c.Writer = buf

	c.JSON(http.StatusOK, gin.H{"task_id": "task_x"})
	buf.flushToClient()

	n, err := buf.WriteString("tail")
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Contains(t, rec.Body.String(), "tail")

	// 幂等：重复 flush 不重复输出
	before := rec.Body.Len()
	buf.flushToClient()
	assert.Equal(t, before, rec.Body.Len())
}

// Header 通过嵌入接口直达真实 writer：缓冲期间设置的响应头（如
// X-New-Api-Other-Ratios、Content-Type）在 flush 时一并发出。
func TestTaskResponseBufferPreservesHeadersSetDuringBuffering(t *testing.T) {
	c, rec := newBufferTestContext(t)
	buf := newTaskResponseBuffer(c.Writer)
	c.Writer = buf

	c.Header("X-New-Api-Other-Ratios", `{"seconds":15}`)
	c.JSON(http.StatusOK, gin.H{"task_id": "task_h"})
	buf.flushToClient()

	assert.Equal(t, `{"seconds":15}`, rec.Header().Get("X-New-Api-Other-Ratios"))
	assert.Equal(t, http.StatusOK, rec.Code)
}
