package controller

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
)

// taskResponseBuffer 包装 gin.ResponseWriter，把 task 提交路径上 adaptor 在
// DoResponse 内部直接写出的响应（一次性小 JSON，无流式）先缓冲住，等控制器完成
// task.Insert() 后再真正写给客户端。
//
// 背景：所有 task adaptor（ali/suno/kling/doubao/hailuo/...）都在 DoResponse 里
// c.JSON 写回含 taskId 的 200 响应，而任务行要等控制器随后
// SettleBilling → LogTaskConsumption → task.Insert() 才入库可查；查询端
// （relay/relay_task.go videoFetchByIDRespBodyBuilder）纯查 DB，查不到返回
// 400 task_not_exist。客户端拿到 taskId 后零延迟首轮轮询会撞上该窗口，
// 造成"上游已成功、客户端误判失败"。缓冲写回使"响应到达客户端"严格晚于
// "任务行可查"，平台无关地消灭该竞态。
//
// flushToClient 之后进入直通模式：所有写操作透传底层 writer，保证残留引用安全。
type taskResponseBuffer struct {
	gin.ResponseWriter
	body        bytes.Buffer
	statusCode  int
	headerWrote bool
	flushed     bool
}

func newTaskResponseBuffer(w gin.ResponseWriter) *taskResponseBuffer {
	return &taskResponseBuffer{ResponseWriter: w}
}

// WriteHeader 缓冲期只记录状态码（首个生效），真正写出推迟到 flushToClient。
func (w *taskResponseBuffer) WriteHeader(code int) {
	if w.flushed {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if !w.headerWrote {
		w.headerWrote = true
		w.statusCode = code
	}
}

// WriteHeaderNow 缓冲期被抑制：若放行，底层 writer 会立刻把自己记录的状态码
// 发上连接，导致 flush 时的真实状态码变成 superfluous。
func (w *taskResponseBuffer) WriteHeaderNow() {
	if w.flushed {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *taskResponseBuffer) Write(data []byte) (int, error) {
	if w.flushed {
		return w.ResponseWriter.Write(data)
	}
	return w.body.Write(data)
}

func (w *taskResponseBuffer) WriteString(s string) (int, error) {
	if w.flushed {
		return w.ResponseWriter.WriteString(s)
	}
	return w.body.WriteString(s)
}

func (w *taskResponseBuffer) Written() bool {
	if w.flushed {
		return w.ResponseWriter.Written()
	}
	return w.headerWrote || w.body.Len() > 0
}

func (w *taskResponseBuffer) Status() int {
	if !w.flushed && w.headerWrote {
		return w.statusCode
	}
	return w.ResponseWriter.Status()
}

func (w *taskResponseBuffer) Size() int {
	if w.flushed {
		return w.ResponseWriter.Size()
	}
	return w.body.Len()
}

// Flush 缓冲期被抑制（提交响应是一次性 JSON，无流式需求）。
func (w *taskResponseBuffer) Flush() {
	if w.flushed {
		w.ResponseWriter.Flush()
	}
}

// reset 丢弃已缓冲但尚未写出的内容。重试下一次尝试前调用，防止失败尝试的
// 残留写入与最终响应串包（真实 writer 无法做到这一点：字节已上连接）。
func (w *taskResponseBuffer) reset() {
	if w.flushed {
		return
	}
	w.headerWrote = false
	w.statusCode = 0
	w.body.Reset()
}

// flushToClient 把缓冲的响应真正写给客户端。幂等；调用后 writer 进入直通模式。
// 缓冲期内没有任何写入时不产生输出（错误路径由 respondTaskError 直写真实 writer）。
func (w *taskResponseBuffer) flushToClient() {
	if w.flushed {
		return
	}
	w.flushed = true
	if !w.headerWrote && w.body.Len() == 0 {
		return
	}
	status := w.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
	if w.body.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
	} else {
		// 无 body 的纯状态码响应：底层 gin writer 会把 header 推迟到首次 body
		// 写入，这里显式落盘，保证状态码一定发出。
		w.ResponseWriter.WriteHeaderNow()
	}
}
