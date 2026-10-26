package tkhttp

// 请求阶段常量用于判断 RequestError.Phase 和日志中的 http.phase。
// 使用字符串常量，兼容现有字段类型及日志值。
const (
	// PhasePrepare 表示参数校验和请求准备阶段。
	PhasePrepare = "prepare"
	// PhaseHook 表示签名或鉴权钩子执行阶段。
	PhaseHook = "hook"
	// PhaseTransport 表示 HTTP 发送阶段。
	PhaseTransport = "transport"
	// PhaseRead 表示响应正文读取阶段。
	PhaseRead = "read"
	// PhaseStatus 表示 HTTP 状态码检查阶段。
	PhaseStatus = "status"
	// PhaseDecode 表示 JSON 响应解码阶段。
	PhaseDecode = "decode"
	// PhaseComplete 表示整次调用成功完成。
	PhaseComplete = "complete"
	// PhaseMiddleware 表示中间件失败或违反调用约定。
	PhaseMiddleware = "middleware"
)
