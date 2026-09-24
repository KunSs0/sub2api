package service

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Gin context keys used by Ops error logger for capturing upstream error details.
// These keys are set by gateway services and consumed by handler/ops_error_logger.go.
const (
	OpsUpstreamStatusCodeKey   = "ops_upstream_status_code"
	OpsUpstreamErrorMessageKey = "ops_upstream_error_message"
	OpsUpstreamErrorDetailKey  = "ops_upstream_error_detail"
	OpsUpstreamErrorsKey       = "ops_upstream_errors"
	OpsUpstreamModelKey        = "ops_upstream_model"

	// Optional stage latencies (milliseconds) for troubleshooting and alerting.
	OpsAuthLatencyMsKey     = "ops_auth_latency_ms"
	OpsRoutingLatencyMsKey  = "ops_routing_latency_ms"
	OpsUpstreamLatencyMsKey = "ops_upstream_latency_ms"
	// OpsUpstreamDispatchOffsetMsKey is the elapsed time from Forward start to
	// the upstream HTTP request being dispatched. It lets access logs split
	// TTFT into request preparation, response-header wait, and post-header wait.
	OpsUpstreamDispatchOffsetMsKey = "ops_upstream_dispatch_offset_ms"
	// OpenAI stream milestones are relative to the request start. They let the
	// access logger and usage row distinguish transport bytes, complete SSE
	// events, semantic output, and visible output.
	OpsUpstreamFirstReadMsKey            = "ops_upstream_first_read_ms"
	OpsUpstreamFirstEventMsKey           = "ops_upstream_first_event_ms"
	OpsOpenAIOutputItemFirstMsKey        = "ops_openai_output_item_first_ms"
	OpsOpenAIInProgressMsKey             = "ops_openai_in_progress_ms"
	OpsOpenAIOutputItemWaitMsKey         = "ops_openai_output_item_wait_ms"
	OpsOpenAISemanticFirstTokenMsKey     = "ops_openai_semantic_first_token_ms"
	OpsOpenAIVisibleFirstTokenMsKey      = "ops_openai_visible_first_token_ms"
	OpsOpenAIAnswerFirstTokenMsKey       = "ops_openai_answer_first_token_ms"
	OpsOpenAIReasoningFirstTokenMsKey    = "ops_openai_reasoning_first_token_ms"
	OpsOpenAIToolFirstTokenMsKey         = "ops_openai_tool_first_token_ms"
	OpsUpstreamFirstEventTypeKey         = "ops_upstream_first_event_type"
	OpsOpenAIOutputItemFirstEventTypeKey = "ops_openai_output_item_first_event_type"
	OpsOpenAIOutputItemFirstTypeKey      = "ops_openai_output_item_first_type"
	OpsOpenAISemanticFirstEventTypeKey   = "ops_openai_semantic_first_event_type"
	OpsOpenAIVisibleFirstEventTypeKey    = "ops_openai_visible_first_event_type"
	OpsOpenAIAnswerFirstEventTypeKey     = "ops_openai_answer_first_event_type"
	OpsOpenAIReasoningFirstEventTypeKey  = "ops_openai_reasoning_first_event_type"
	OpsOpenAIToolFirstEventTypeKey       = "ops_openai_tool_first_event_type"
	OpsUpstreamCompletedMsKey            = "ops_upstream_completed_ms"
	OpsUpstreamEventCountKey             = "ops_upstream_event_count"
	OpsUpstreamMaxEventGapMsKey          = "ops_upstream_max_event_gap_ms"
	OpsUpstreamMaxEventGapFromTypeKey    = "ops_upstream_max_event_gap_from_type"
	OpsUpstreamMaxEventGapToTypeKey      = "ops_upstream_max_event_gap_to_type"
	OpsOpenAIStreamLastEventAtKey        = "ops_openai_stream_last_event_at"
	OpsOpenAIStreamLastEventTypeKey      = "ops_openai_stream_last_event_type"
	OpsUpstreamRequestIDKey              = "ops_upstream_request_id"
	OpsResponseLatencyMsKey              = "ops_response_latency_ms"
	OpsTimeToFirstTokenMsKey             = "ops_time_to_first_token_ms"
	// OpenAI WS 关键观测字段
	OpsOpenAIWSQueueWaitMsKey = "ops_openai_ws_queue_wait_ms"
	OpsOpenAIWSConnPickMsKey  = "ops_openai_ws_conn_pick_ms"
	OpsOpenAIWSConnReusedKey  = "ops_openai_ws_conn_reused"
	OpsOpenAIWSConnIDKey      = "ops_openai_ws_conn_id"

	// OpsSkipPassthroughKey 由 applyErrorPassthroughRule 在命中 skip_monitoring=true 的规则时设置。
	// ops_error_logger 中间件检查此 key，为 true 时跳过错误记录。
	OpsSkipPassthroughKey = "ops_skip_passthrough"

	// OpsStreamErrorKey 保存 handleStreamingAwareError 在「响应已固化为 HTTP 200 的 SSE 流」
	// 上就地(in-band)补发错误帧时记录的 OpsStreamError。因为 wire 状态码停留在 200，
	// ops_error_logger 的 status>=400 采集路径永远不会触发，这类流内失败
	//（例如等待并发槽位超时后回退的限流、Wait 后二次计费校验失败）本会在错误看板里隐形。
	OpsStreamErrorKey  = "ops_stream_error"
	OpsStreamErrorsKey = "ops_stream_errors"
	OpsStreamTurnKey   = "ops_stream_turn"

	// Client-side configuration denials should remain visible in ops_error_logs,
	// but should be excluded from SLA/error-rate calculations.
	// ResponseCommittedKey 由 handleErrorResponse 系列函数在写完 HTTP 错误响应后设置。
	// ensureForwardErrorResponse 检查此 key，为 true 时跳过兜底写入，避免在已完成的 JSON 后追加 SSE。
	ResponseCommittedKey = "response_committed"

	OpsClientBusinessLimitedKey                           = "ops_client_business_limited"
	OpsClientBusinessLimitedReasonKey                     = "ops_client_business_limited_reason"
	OpsClientBusinessLimitedReasonIPRestriction           = "api_key_ip_restriction"
	OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable  = "api_key_group_unavailable"
	OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned   = "api_key_group_unassigned"
	OpsClientBusinessLimitedReasonLocalFeatureGate        = "local_feature_gate"
	OpsClientBusinessLimitedReasonLocalPolicyDenied       = "local_policy_denied"
	OpsClientBusinessLimitedReasonLocalModelConfiguration = "local_model_configuration"
)

func MarkResponseCommitted(c *gin.Context) { c.Set(ResponseCommittedKey, true) }

func IsResponseCommitted(c *gin.Context) bool {
	v, ok := c.Get(ResponseCommittedKey)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func SetOpsLatencyMs(c *gin.Context, key string, value int64) {
	if c == nil || strings.TrimSpace(key) == "" || value < 0 {
		return
	}
	c.Set(key, value)
}

// SetOpsString stores a request-local diagnostic string. Empty values are
// allowed so a later upstream attempt can explicitly clear a stale value.
func SetOpsString(c *gin.Context, key, value string) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}
	c.Set(key, strings.TrimSpace(value))
}

// GetOpsString reads a request-local diagnostic string.
func GetOpsString(c *gin.Context, key string) (string, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return "", false
	}
	v, ok := c.Get(key)
	if !ok {
		return "", false
	}
	value, ok := v.(string)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

// GetOpsLatencyMs reads a latency value previously stored in the Gin context.
// Values are kept tolerant of the integer types used by handlers and tests so
// consumers such as the access logger do not need to duplicate this parsing.
func GetOpsLatencyMs(c *gin.Context, key string) (int64, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return 0, false
	}
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch value := v.(type) {
	case int64:
		return value, value >= 0
	case int:
		return int64(value), value >= 0
	case int32:
		return int64(value), value >= 0
	case int16:
		return int64(value), value >= 0
	case int8:
		return int64(value), value >= 0
	case uint64:
		return int64(value), value <= uint64(^uint64(0)>>1)
	case uint:
		return int64(value), uint64(value) <= uint64(^uint64(0)>>1)
	case uint32:
		return int64(value), true
	case uint16:
		return int64(value), true
	case uint8:
		return int64(value), true
	case float64:
		return int64(value), value >= 0
	case float32:
		return int64(value), value >= 0
	default:
		return 0, false
	}
}

// UsageTimingBreakdown is the request timing snapshot persisted with a usage
// row. The top-level usage_log duration/first_token fields remain the stable
// summary values; this optional object carries the diagnostic stages needed to
// explain a slow first token without parsing server logs.
type UsageTimingBreakdown struct {
	AuthLatencyMs               *int   `json:"auth_latency_ms,omitempty"`
	RoutingLatencyMs            *int   `json:"routing_latency_ms,omitempty"`
	UpstreamDispatchOffsetMs    *int   `json:"upstream_dispatch_offset_ms,omitempty"`
	UpstreamHeaderLatencyMs     *int   `json:"upstream_header_latency_ms,omitempty"`
	UpstreamFirstReadMs         *int   `json:"upstream_first_read_ms,omitempty"`
	UpstreamFirstEventMs        *int   `json:"upstream_first_event_ms,omitempty"`
	OutputItemFirstMs           *int   `json:"output_item_first_ms,omitempty"`
	InProgressMs                *int   `json:"in_progress_ms,omitempty"`
	OutputItemWaitMs            *int   `json:"output_item_wait_ms,omitempty"`
	SemanticFirstTokenMs        *int   `json:"semantic_first_token_ms,omitempty"`
	VisibleFirstTokenMs         *int   `json:"visible_first_token_ms,omitempty"`
	AnswerFirstTokenMs          *int   `json:"answer_first_token_ms,omitempty"`
	ReasoningFirstTokenMs       *int   `json:"reasoning_first_token_ms,omitempty"`
	ToolFirstTokenMs            *int   `json:"tool_first_token_ms,omitempty"`
	UpstreamFirstEventType      string `json:"upstream_first_event_type,omitempty"`
	OutputItemFirstEventType    string `json:"output_item_first_event_type,omitempty"`
	OutputItemFirstType         string `json:"output_item_first_type,omitempty"`
	SemanticFirstEventType      string `json:"semantic_first_event_type,omitempty"`
	VisibleFirstEventType       string `json:"visible_first_event_type,omitempty"`
	AnswerFirstEventType        string `json:"answer_first_event_type,omitempty"`
	ReasoningFirstEventType     string `json:"reasoning_first_event_type,omitempty"`
	ToolFirstEventType          string `json:"tool_first_event_type,omitempty"`
	UpstreamCompletedMs         *int   `json:"upstream_completed_ms,omitempty"`
	UpstreamEventCount          *int   `json:"upstream_event_count,omitempty"`
	UpstreamMaxEventGapMs       *int   `json:"upstream_max_event_gap_ms,omitempty"`
	UpstreamMaxEventGapFromType string `json:"upstream_max_event_gap_from_type,omitempty"`
	UpstreamMaxEventGapToType   string `json:"upstream_max_event_gap_to_type,omitempty"`
	UpstreamWaitAfterHeadersMs  *int   `json:"upstream_wait_after_headers_ms,omitempty"`
	FirstTokenMs                *int   `json:"first_token_ms,omitempty"`
	AfterFirstTokenMs           *int   `json:"after_first_token_ms,omitempty"`
	ForwardLatencyMs            *int   `json:"forward_latency_ms,omitempty"`
	ResponseLatencyMs           *int   `json:"response_latency_ms,omitempty"`
}

// CaptureUsageTimingBreakdown snapshots the timing values accumulated in the
// request context. It is intentionally tolerant of missing stages because
// different protocols do not all expose the same milestones.
func CaptureUsageTimingBreakdown(c *gin.Context, forwardDurationMs int64) *UsageTimingBreakdown {
	if c == nil || forwardDurationMs < 0 {
		return nil
	}

	toInt := func(value int64) *int {
		converted := int(value)
		return &converted
	}
	read := func(key string) *int {
		value, ok := GetOpsLatencyMs(c, key)
		if !ok {
			return nil
		}
		return toInt(value)
	}
	readString := func(key string) string {
		value, _ := GetOpsString(c, key)
		return value
	}

	out := &UsageTimingBreakdown{
		AuthLatencyMs:               read(OpsAuthLatencyMsKey),
		RoutingLatencyMs:            read(OpsRoutingLatencyMsKey),
		UpstreamDispatchOffsetMs:    read(OpsUpstreamDispatchOffsetMsKey),
		UpstreamHeaderLatencyMs:     read(OpsUpstreamLatencyMsKey),
		UpstreamFirstReadMs:         read(OpsUpstreamFirstReadMsKey),
		UpstreamFirstEventMs:        read(OpsUpstreamFirstEventMsKey),
		OutputItemFirstMs:           read(OpsOpenAIOutputItemFirstMsKey),
		InProgressMs:                read(OpsOpenAIInProgressMsKey),
		OutputItemWaitMs:            read(OpsOpenAIOutputItemWaitMsKey),
		SemanticFirstTokenMs:        read(OpsOpenAISemanticFirstTokenMsKey),
		VisibleFirstTokenMs:         read(OpsOpenAIVisibleFirstTokenMsKey),
		AnswerFirstTokenMs:          read(OpsOpenAIAnswerFirstTokenMsKey),
		ReasoningFirstTokenMs:       read(OpsOpenAIReasoningFirstTokenMsKey),
		ToolFirstTokenMs:            read(OpsOpenAIToolFirstTokenMsKey),
		UpstreamFirstEventType:      readString(OpsUpstreamFirstEventTypeKey),
		OutputItemFirstEventType:    readString(OpsOpenAIOutputItemFirstEventTypeKey),
		OutputItemFirstType:         readString(OpsOpenAIOutputItemFirstTypeKey),
		SemanticFirstEventType:      readString(OpsOpenAISemanticFirstEventTypeKey),
		VisibleFirstEventType:       readString(OpsOpenAIVisibleFirstEventTypeKey),
		AnswerFirstEventType:        readString(OpsOpenAIAnswerFirstEventTypeKey),
		ReasoningFirstEventType:     readString(OpsOpenAIReasoningFirstEventTypeKey),
		ToolFirstEventType:          readString(OpsOpenAIToolFirstEventTypeKey),
		UpstreamCompletedMs:         read(OpsUpstreamCompletedMsKey),
		UpstreamEventCount:          read(OpsUpstreamEventCountKey),
		UpstreamMaxEventGapMs:       read(OpsUpstreamMaxEventGapMsKey),
		UpstreamMaxEventGapFromType: readString(OpsUpstreamMaxEventGapFromTypeKey),
		UpstreamMaxEventGapToType:   readString(OpsUpstreamMaxEventGapToTypeKey),
		ResponseLatencyMs:           read(OpsResponseLatencyMsKey),
		FirstTokenMs:                read(OpsTimeToFirstTokenMsKey),
		ForwardLatencyMs:            toInt(forwardDurationMs),
	}

	if out.FirstTokenMs != nil && out.ForwardLatencyMs != nil && *out.ForwardLatencyMs >= *out.FirstTokenMs {
		afterFirstTokenMs := *out.ForwardLatencyMs - *out.FirstTokenMs
		out.AfterFirstTokenMs = &afterFirstTokenMs
	}
	if out.FirstTokenMs != nil && out.UpstreamDispatchOffsetMs != nil && out.UpstreamHeaderLatencyMs != nil {
		waitAfterHeadersMs := *out.FirstTokenMs - *out.UpstreamDispatchOffsetMs - *out.UpstreamHeaderLatencyMs
		if waitAfterHeadersMs >= 0 {
			out.UpstreamWaitAfterHeadersMs = &waitAfterHeadersMs
		}
	}

	if out.AuthLatencyMs == nil && out.RoutingLatencyMs == nil && out.UpstreamDispatchOffsetMs == nil &&
		out.UpstreamHeaderLatencyMs == nil && out.UpstreamFirstReadMs == nil && out.UpstreamFirstEventMs == nil &&
		out.OutputItemFirstMs == nil && out.InProgressMs == nil && out.OutputItemWaitMs == nil &&
		out.SemanticFirstTokenMs == nil && out.VisibleFirstTokenMs == nil &&
		out.AnswerFirstTokenMs == nil && out.ReasoningFirstTokenMs == nil && out.ToolFirstTokenMs == nil &&
		out.UpstreamCompletedMs == nil && out.UpstreamEventCount == nil && out.UpstreamMaxEventGapMs == nil &&
		out.UpstreamWaitAfterHeadersMs == nil &&
		out.FirstTokenMs == nil && out.AfterFirstTokenMs == nil && out.ResponseLatencyMs == nil &&
		out.UpstreamFirstEventType == "" && out.OutputItemFirstEventType == "" && out.OutputItemFirstType == "" &&
		out.SemanticFirstEventType == "" && out.VisibleFirstEventType == "" && out.AnswerFirstEventType == "" &&
		out.ReasoningFirstEventType == "" && out.ToolFirstEventType == "" &&
		out.UpstreamMaxEventGapFromType == "" && out.UpstreamMaxEventGapToType == "" {
		return nil
	}
	return out
}

// SetOpsUpstreamModel stores only the effective model slug for final Ops
// attribution. Call it immediately before an upstream attempt is dispatched.
func SetOpsUpstreamModel(c *gin.Context, model string) {
	if c == nil {
		return
	}
	if model = strings.TrimSpace(model); model != "" {
		c.Set(OpsUpstreamModelKey, model)
	}
}

// ClearOpsUpstreamModel invalidates attempt-scoped model attribution before a
// newly selected account starts credential resolution or upstream dispatch.
func ClearOpsUpstreamModel(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(OpsUpstreamModelKey, "")
}

func MarkOpsClientBusinessLimited(c *gin.Context, reason string) {
	if c == nil {
		return
	}
	c.Set(OpsClientBusinessLimitedKey, true)
	if reason = strings.TrimSpace(reason); reason != "" {
		c.Set(OpsClientBusinessLimitedReasonKey, reason)
	}
}

func HasOpsClientBusinessLimited(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(OpsClientBusinessLimitedKey)
	if !ok {
		return false
	}
	marked, _ := v.(bool)
	return marked
}

func OpsClientBusinessLimitedReason(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(OpsClientBusinessLimitedReasonKey)
	if !ok {
		return ""
	}
	reason, _ := v.(string)
	return strings.TrimSpace(reason)
}

// OpsStreamError 描述承载在 2xx 响应上的带内错误：网关在响应状态已固化为 200 之后
// 就地以 SSE error 帧返回的错误（并发限流回退、Wait 后二次计费校验失败、流开始后才无
// 可用账号等），以及上游 2xx 正文或事件里携带的错误结果（NonStream 标记非流式正文）。
// 由于 HTTP 状态码停留在 2xx，ops_error_logger 中间件在 status<400 分支消费该标记并
// 补记错误日志；标记方是 handler.handleStreamingAwareError 或各 service 的带内检测。
type OpsStreamError struct {
	// ErrType 是写入 SSE 帧的对客错误类型（如 rate_limit_error / upstream_error / api_error）。
	ErrType string
	// Code 是可选的稳定错误分类；用于既保留通用 OpenAI error.type，又向客户端和 Ops
	// 暴露可编程判断的细分类（如 upstream_http2_stream_error）。
	Code string
	// Message 是写入 SSE 帧的对客错误消息。
	Message string
	// IntendedStatus 是流若未固化本应返回的 HTTP 状态码（如并发限流的 429）。
	// 默认仅用于错误分级；CountTowardsSLA 或 RequestScoped 为 true 时也作为 Ops 的逻辑状态码。
	IntendedStatus int
	// CountTowardsSLA 表示虽然 wire 状态已固化为 200，请求在应用语义上仍然失败，
	// Ops 应使用 IntendedStatus 计入错误率/SLA。
	CountTowardsSLA bool
	// Turn identifies a WebSocket turn. HTTP/SSE requests leave it at zero.
	Turn int
	// SkipMonitoring snapshots the rule decision for this visible failure.
	SkipMonitoring  bool
	AccountID       int64
	UpstreamModel   string
	UpstreamStatus  int
	UpstreamMessage string
	UpstreamDetail  string
	UpstreamErrors  []*OpsUpstreamErrorEvent
	// RequestScoped 表示该带内失败是请求级结果（如上游内容策略截停），与本请求此前的
	// 上游尝试无关：分类不受上游错误上下文影响、不快照也不落库上游归因、不继承透传规则的
	// skip_monitoring，按业务限制计，落库状态取 IntendedStatus 以便进入错误列表。
	RequestScoped bool
	// NonStream 表示带内信号来自非流式 2xx 响应体，落库 stream=false。
	NonStream bool
}

const maxOpsStreamErrorsPerRequest = 64

// BeginOpsStreamTurn scopes first-wins deduplication to one WebSocket turn.
func BeginOpsStreamTurn(c *gin.Context, turn int) {
	if c == nil || turn <= 0 {
		return
	}
	c.Set(OpsStreamTurnKey, turn)
	// Rule and attempt state is turn-scoped on a long-lived WS connection.
	c.Set(OpsSkipPassthroughKey, false)
	c.Set(OpsUpstreamErrorsKey, []*OpsUpstreamErrorEvent{})
	c.Set(OpsUpstreamStatusCodeKey, 0)
	c.Set(OpsUpstreamErrorMessageKey, "")
	c.Set(OpsUpstreamErrorDetailKey, "")
}

// MarkOpsStreamError 记录一次就地 SSE 错误，供 ops 日志采集。
// 采用「首个标记生效」策略：同一请求若先后补发多帧（如上游透传错误后又追加通用兜底帧），
// 保留最先记录的根因错误，而不是被后续的 "Upstream request failed" 覆盖。
func MarkOpsStreamError(c *gin.Context, errType, message string, intendedStatus int) {
	markOpsStreamError(c, OpsStreamError{
		ErrType:        errType,
		Message:        message,
		IntendedStatus: intendedStatus,
	})
}

// MarkOpsStreamFailure records an in-band stream error that represents a failed
// request and therefore must count towards Ops error rate/SLA despite HTTP 200
// already being committed on the wire.
func MarkOpsStreamFailure(c *gin.Context, errType, code, message string, intendedStatus int) {
	markOpsStreamError(c, OpsStreamError{
		ErrType:         errType,
		Code:            code,
		Message:         message,
		IntendedStatus:  intendedStatus,
		CountTowardsSLA: true,
	})
}

// MarkOpsStreamErrorValue 以完整的 OpsStreamError 记录一次带内错误，供需要
// RequestScoped / NonStream 等附加语义的调用方使用；首个标记生效的规则不变。
// 调用方只填 ErrType / Code / Message / IntendedStatus / CountTowardsSLA / RequestScoped / NonStream。
// AccountID、UpstreamModel 与 Turn 由请求上下文接管；UpstreamStatus、UpstreamMessage、
// UpstreamDetail、UpstreamErrors 与 SkipMonitoring 在非 RequestScoped 时由上下文接管，
// RequestScoped 时被忽略。
func MarkOpsStreamErrorValue(c *gin.Context, streamErr OpsStreamError) {
	markOpsStreamError(c, streamErr)
}

func markOpsStreamError(c *gin.Context, streamErr OpsStreamError) {
	if c == nil {
		return
	}
	streamErr.ErrType = strings.TrimSpace(streamErr.ErrType)
	streamErr.Code = strings.TrimSpace(streamErr.Code)
	streamErr.Message = strings.TrimSpace(streamErr.Message)
	if !streamErr.RequestScoped {
		streamErr.SkipMonitoring = currentOpsFailureSkipMonitoring(c)
	}
	snapshotOpsStreamErrorContext(c, &streamErr)
	if GetOpenAIClientTransport(c) == OpenAIClientTransportWS {
		if value, ok := c.Get(OpsStreamTurnKey); ok {
			streamErr.Turn, _ = value.(int)
		}
		var errorsForRequest []OpsStreamError
		if value, ok := c.Get(OpsStreamErrorsKey); ok {
			errorsForRequest, _ = value.([]OpsStreamError)
		}
		if len(errorsForRequest) > 0 && errorsForRequest[len(errorsForRequest)-1].Turn == streamErr.Turn {
			return
		}
		errorsForRequest = append(errorsForRequest, streamErr)
		if len(errorsForRequest) > maxOpsStreamErrorsPerRequest {
			errorsForRequest = append([]OpsStreamError(nil), errorsForRequest[len(errorsForRequest)-maxOpsStreamErrorsPerRequest:]...)
		}
		c.Set(OpsStreamErrorsKey, errorsForRequest)
		c.Set(OpsStreamErrorKey, streamErr)
		return
	}
	if _, exists := c.Get(OpsStreamErrorKey); exists {
		return
	}
	c.Set(OpsStreamErrorKey, streamErr)
}

func snapshotOpsStreamErrorContext(c *gin.Context, streamErr *OpsStreamError) {
	if c == nil || streamErr == nil {
		return
	}
	if c.Request != nil {
		if accountID, ok := c.Request.Context().Value(ctxkey.AccountID).(int64); ok && accountID > 0 {
			streamErr.AccountID = accountID
		}
	}
	if value, ok := c.Get(OpsUpstreamModelKey); ok {
		streamErr.UpstreamModel, _ = value.(string)
		streamErr.UpstreamModel = strings.TrimSpace(streamErr.UpstreamModel)
	}
	if streamErr.RequestScoped {
		return
	}
	if value, ok := c.Get(OpsUpstreamStatusCodeKey); ok {
		switch status := value.(type) {
		case int:
			streamErr.UpstreamStatus = status
		case int64:
			streamErr.UpstreamStatus = int(status)
		}
	}
	if value, ok := c.Get(OpsUpstreamErrorMessageKey); ok {
		streamErr.UpstreamMessage, _ = value.(string)
		streamErr.UpstreamMessage = strings.TrimSpace(streamErr.UpstreamMessage)
	}
	if value, ok := c.Get(OpsUpstreamErrorDetailKey); ok {
		streamErr.UpstreamDetail, _ = value.(string)
		streamErr.UpstreamDetail = strings.TrimSpace(streamErr.UpstreamDetail)
	}
	if value, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if events, ok := value.([]*OpsUpstreamErrorEvent); ok {
			streamErr.UpstreamErrors = make([]*OpsUpstreamErrorEvent, 0, len(events))
			for _, event := range events {
				if event == nil {
					streamErr.UpstreamErrors = append(streamErr.UpstreamErrors, nil)
					continue
				}
				copyOfEvent := *event
				streamErr.UpstreamErrors = append(streamErr.UpstreamErrors, &copyOfEvent)
			}
		}
	}
}

func currentOpsFailureSkipMonitoring(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if value, ok := c.Get(OpsSkipPassthroughKey); ok {
		if skip, _ := value.(bool); skip {
			return true
		}
	}
	if value, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if events, ok := value.([]*OpsUpstreamErrorEvent); ok {
			for i := len(events) - 1; i >= 0; i-- {
				if events[i] != nil {
					return events[i].SkipMonitoring
				}
			}
		}
	}
	return false
}

// GetOpsStreamError 返回本请求记录的就地 SSE 错误（若有）。
func GetOpsStreamError(c *gin.Context) (OpsStreamError, bool) {
	if c == nil {
		return OpsStreamError{}, false
	}
	v, ok := c.Get(OpsStreamErrorKey)
	if !ok {
		return OpsStreamError{}, false
	}
	se, ok := v.(OpsStreamError)
	return se, ok
}

func GetOpsStreamErrors(c *gin.Context) []OpsStreamError {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(OpsStreamErrorsKey); ok {
		if errorsForRequest, ok := value.([]OpsStreamError); ok && len(errorsForRequest) > 0 {
			return append([]OpsStreamError(nil), errorsForRequest...)
		}
	}
	if streamErr, ok := GetOpsStreamError(c); ok {
		return []OpsStreamError{streamErr}
	}
	return nil
}

// SetOpsUpstreamError is the exported wrapper for setOpsUpstreamError, used by
// handler-layer code (e.g. failover-exhausted paths) that needs to record the
// original upstream status code before mapping it to a client-facing code.
func SetOpsUpstreamError(c *gin.Context, upstreamStatusCode int, upstreamMessage, upstreamDetail string) {
	setOpsUpstreamError(c, upstreamStatusCode, upstreamMessage, upstreamDetail)
}

func setOpsUpstreamError(c *gin.Context, upstreamStatusCode int, upstreamMessage, upstreamDetail string) {
	if c == nil {
		return
	}
	if upstreamStatusCode > 0 {
		c.Set(OpsUpstreamStatusCodeKey, upstreamStatusCode)
	}
	if msg := strings.TrimSpace(upstreamMessage); msg != "" {
		c.Set(OpsUpstreamErrorMessageKey, msg)
	}
	if detail := strings.TrimSpace(upstreamDetail); detail != "" {
		c.Set(OpsUpstreamErrorDetailKey, detail)
	}
}

// OpsUpstreamErrorEvent describes one upstream error attempt during a single gateway request.
// It is stored in ops_error_logs.upstream_errors as a JSON array.
type OpsUpstreamErrorEvent struct {
	AtUnixMs int64 `json:"at_unix_ms,omitempty"`

	// Passthrough 表示本次请求是否命中“原样透传（仅替换认证）”分支。
	// 该字段用于排障与灰度评估；存入 JSON，不涉及 DB schema 变更。
	Passthrough bool `json:"passthrough,omitempty"`

	// Context
	Platform    string `json:"platform,omitempty"`
	AccountID   int64  `json:"account_id,omitempty"`
	AccountName string `json:"account_name,omitempty"`

	// Proxy attribution is an immutable, credential-free snapshot of the route
	// used by this attempt. ProxyID is null for direct and unknown routes;
	// ProxyName distinguishes direct/no_proxy from unknown.
	// Never add proxy URLs or credentials to this event.
	ProxyID   *int64 `json:"proxy_id"`
	ProxyName string `json:"proxy_name"`

	// DroppedEarlierAttempts is set on the oldest retained event when queue
	// bounds forced earlier attempts of the same request to be discarded.
	DroppedEarlierAttempts int `json:"dropped_earlier_attempts,omitempty"`

	// Outcome
	UpstreamStatusCode int    `json:"upstream_status_code,omitempty"`
	UpstreamRequestID  string `json:"upstream_request_id,omitempty"`

	// UpstreamURL is the actual upstream URL that was called (host + path, query/fragment stripped).
	// Helps debug 404/routing errors by showing which endpoint was targeted.
	UpstreamURL string `json:"upstream_url,omitempty"`

	// Best-effort upstream response capture (sanitized+trimmed).
	UpstreamResponseBody string `json:"upstream_response_body,omitempty"`

	// Kind: http_error | request_error | retry_exhausted | failover
	Kind string `json:"kind,omitempty"`
	// Stage/Scope/Reason distinguish credential acquisition from inference
	// without overloading upstream_status_code with a synthetic HTTP status.
	Stage  string `json:"stage,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Reason string `json:"reason,omitempty"`

	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`

	// SkipMonitoring is request-local rule state. It is intentionally excluded
	// from persisted attempt JSON. The logger consults it only when this event is
	// the final client-visible failure; recovered attempts remain provider-health
	// telemetry and do not count as failed requests.
	SkipMonitoring bool `json:"-"`
}

const (
	opsProxyNameDirect  = "direct/no_proxy"
	opsProxyNameUnknown = "unknown"
	// opsProxyNameUnnamed labels a managed proxy whose name is blank. The
	// proxies.name column is NOT NULL/non-empty, so this is a defensive value.
	opsProxyNameUnnamed = "proxy"
)

func appendOpsUpstreamError(c *gin.Context, ev OpsUpstreamErrorEvent) {
	if c == nil {
		return
	}
	if ev.AtUnixMs <= 0 {
		ev.AtUnixMs = time.Now().UnixMilli()
	}
	ev.Platform = strings.TrimSpace(ev.Platform)
	normalizeOpsUpstreamProxyAttribution(&ev)
	ev.UpstreamRequestID = strings.TrimSpace(ev.UpstreamRequestID)
	ev.UpstreamResponseBody = strings.TrimSpace(ev.UpstreamResponseBody)
	ev.Kind = strings.TrimSpace(ev.Kind)
	ev.Stage = strings.TrimSpace(ev.Stage)
	ev.Scope = strings.TrimSpace(ev.Scope)
	ev.Reason = strings.TrimSpace(ev.Reason)
	ev.UpstreamURL = strings.TrimSpace(ev.UpstreamURL)
	ev.Message = strings.TrimSpace(ev.Message)
	ev.Detail = strings.TrimSpace(ev.Detail)
	if ev.Message != "" {
		ev.Message = sanitizeUpstreamErrorMessage(ev.Message)
	}

	var existing []*OpsUpstreamErrorEvent
	if v, ok := c.Get(OpsUpstreamErrorsKey); ok {
		if arr, ok := v.([]*OpsUpstreamErrorEvent); ok {
			existing = arr
		}
	}

	evCopy := ev
	existing = append(existing, &evCopy)
	c.Set(OpsUpstreamErrorsKey, existing)

	checkSkipMonitoringForUpstreamEvent(c, &evCopy)
}

// opsUpstreamProxyAttribution derives both attribution fields from one
// decision so they can never disagree. The event is assembled at the failure
// site; forwarding code builds managed proxy routes from Proxy only when the
// binding ID is also set, so the same rule decides the label here:
//
//   - nil account                     -> (nil, unknown)
//   - no binding or no hydrated Proxy -> (nil, direct/no_proxy)
//   - hydrated Proxy without durable ID -> (nil, unknown): the transport did
//     use that proxy, but nothing durable identifies it
//   - otherwise                       -> (Proxy.ID, Proxy.Name or "proxy")
//
// Invariant: proxy_id == null implies proxy_name is one of the two sentinels.
func opsUpstreamProxyAttribution(account *Account) (*int64, string) {
	if account == nil {
		return nil, opsProxyNameUnknown
	}
	if account.ProxyID == nil || account.Proxy == nil {
		return nil, opsProxyNameDirect
	}
	if account.Proxy.ID <= 0 {
		return nil, opsProxyNameUnknown
	}
	proxyID := account.Proxy.ID
	name := strings.TrimSpace(account.Proxy.Name)
	if name == "" {
		name = opsProxyNameUnnamed
	}
	return &proxyID, name
}

func opsUpstreamProxyID(account *Account) *int64 {
	proxyID, _ := opsUpstreamProxyAttribution(account)
	return proxyID
}

func opsUpstreamProxyName(account *Account) string {
	_, name := opsUpstreamProxyAttribution(account)
	return name
}

// opsUpstreamWSProxyAttribution is the OpenAI WebSocket variant. The WS dialer
// sets an explicit proxy client only when the account has a usable managed
// proxy; otherwise coder/websocket falls back to http.DefaultClient, which
// honors HTTP_PROXY/HTTPS_PROXY/NO_PROXY. A missing managed proxy is therefore
// unknown, not direct.
func opsUpstreamWSProxyAttribution(account *Account) (*int64, string) {
	proxyID, name := opsUpstreamProxyAttribution(account)
	if proxyID == nil {
		return nil, opsProxyNameUnknown
	}
	return proxyID, name
}

// usageLogProxyAttribution snapshots the outbound proxy for usage_logs. It uses
// the same decision as the HTTP transports that dial upstream, plus one
// usage-specific case: an Anthropic custom base URL relay receives the proxy URL
// as a query parameter and dials upstream itself, so sub2api cannot prove the
// egress route and the row is marked unknown rather than direct.
//
// Host and port are only reported for a managed proxy, and never carry a scheme
// or credentials.
func usageLogProxyAttribution(account *Account) (proxyID *int64, name string, host string, port *int) {
	if account != nil && account.IsCustomBaseURLEnabled() && account.GetCustomBaseURL() != "" {
		return nil, opsProxyNameUnknown, "", nil
	}
	id, label := opsUpstreamProxyAttribution(account)
	return usageLogProxySnapshot(id, label, account)
}

// usageLogWSProxyAttribution is the WebSocket counterpart for usage_logs. A WS
// request without a usable managed proxy falls back to the default client (which
// honors HTTP_PROXY/HTTPS_PROXY/NO_PROXY), so the route is unknown, not direct.
func usageLogWSProxyAttribution(account *Account) (proxyID *int64, name string, host string, port *int) {
	id, label := opsUpstreamWSProxyAttribution(account)
	return usageLogProxySnapshot(id, label, account)
}

// usageLogProxySnapshot pairs an attribution decision with the endpoint
// snapshot. Host and port are only reported when the decision names a managed
// proxy, so the stored columns can never disagree with proxy_id.
func usageLogProxySnapshot(proxyID *int64, name string, account *Account) (id *int64, proxyName string, host string, port *int) {
	if proxyID == nil {
		return nil, name, "", nil
	}
	if account == nil || account.Proxy == nil {
		return proxyID, name, "", nil
	}
	host = strings.TrimSpace(account.Proxy.Host)
	if account.Proxy.Port > 0 {
		value := account.Proxy.Port
		port = &value
	}
	return proxyID, name, host, port
}

func setUnknownOpsUpstreamProxy(ev *OpsUpstreamErrorEvent) {
	if ev == nil {
		return
	}
	ev.ProxyID = nil
	ev.ProxyName = opsProxyNameUnknown
}

// normalizeOpsUpstreamProxyAttribution makes legacy events explicit without
// pretending that the account's current proxy is historical evidence.
func normalizeOpsUpstreamProxyAttribution(ev *OpsUpstreamErrorEvent) {
	if ev == nil {
		return
	}
	if ev.ProxyID != nil && *ev.ProxyID <= 0 {
		// A non-positive ID never identifies a managed proxy.
		ev.ProxyID = nil
	}
	ev.ProxyName = strings.TrimSpace(ev.ProxyName)
	if ev.ProxyID != nil {
		if ev.ProxyName == "" {
			ev.ProxyName = opsProxyNameUnnamed
		}
		return
	}
	// Invariant: proxy_id == null implies a sentinel name. Any other name
	// without a durable ID is not historical evidence of a managed route.
	if ev.ProxyName != opsProxyNameDirect {
		setUnknownOpsUpstreamProxy(ev)
	}
}

// checkSkipMonitoringForUpstreamEvent snapshots whether this attempt matches a
// skip_monitoring passthrough rule. The final failure decides whether the
// request error is hidden; an intermediate recovered attempt cannot suppress a
// later client-visible failure.
func checkSkipMonitoringForUpstreamEvent(c *gin.Context, ev *OpsUpstreamErrorEvent) {
	if ev.UpstreamStatusCode == 0 {
		return
	}

	svc := getBoundErrorPassthroughService(c)
	if svc == nil {
		return
	}

	// Use the best available body representation for keyword matching.
	// Even when body is empty, MatchRule can still match rules that only
	// specify ErrorCodes (no Keywords), so we always call it.
	body := ev.Detail
	if body == "" {
		body = ev.Message
	}

	rule := svc.MatchRule(ev.Platform, ev.UpstreamStatusCode, []byte(body))
	if rule != nil && rule.SkipMonitoring {
		ev.SkipMonitoring = true
	}
}

func marshalOpsUpstreamErrors(events []*OpsUpstreamErrorEvent) *string {
	if len(events) == 0 {
		return nil
	}
	// Ensure we always store a valid JSON value.
	raw, err := json.Marshal(events)
	if err != nil || len(raw) == 0 {
		return nil
	}
	s := string(raw)
	return &s
}

func ParseOpsUpstreamErrors(raw string) ([]*OpsUpstreamErrorEvent, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []*OpsUpstreamErrorEvent{}, nil
	}
	var out []*OpsUpstreamErrorEvent
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	for _, ev := range out {
		normalizeOpsUpstreamProxyAttribution(ev)
	}
	return out, nil
}

// normalizeOpsUpstreamErrorsJSON materializes missing proxy attribution on
// stored JSON for detail reads. It edits only the attribution keys of events
// that lack them, so keys written by older struct versions and the original
// key order survive; nothing is re-marshaled through the current struct.
func normalizeOpsUpstreamErrorsJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw, nil
	}
	if !gjson.Valid(raw) {
		return "", errors.New("upstream_errors is not valid JSON")
	}
	parsed := gjson.Parse(raw)
	if !parsed.IsArray() {
		return "", errors.New("upstream_errors is not a JSON array")
	}
	out := raw
	for i, ev := range parsed.Array() {
		if !ev.IsObject() {
			continue
		}
		prefix := strconv.Itoa(i) + "."
		proxyID := ev.Get("proxy_id")
		proxyName := strings.TrimSpace(ev.Get("proxy_name").String())
		hasValidID := proxyID.Exists() && proxyID.Type == gjson.Number && proxyID.Int() > 0
		var err error
		switch {
		case hasValidID:
			if proxyName == "" {
				out, err = sjson.Set(out, prefix+"proxy_name", opsProxyNameUnnamed)
			}
		case proxyName == opsProxyNameDirect:
			if !proxyID.Exists() || proxyID.Type != gjson.Null {
				out, err = sjson.Set(out, prefix+"proxy_id", nil)
			}
		default:
			if out, err = sjson.Set(out, prefix+"proxy_id", nil); err == nil {
				out, err = sjson.Set(out, prefix+"proxy_name", opsProxyNameUnknown)
			}
		}
		if err != nil {
			return "", err
		}
	}
	return out, nil
}

// safeUpstreamURL returns scheme + host + path from a URL, stripping query/fragment
// to avoid leaking sensitive query parameters (e.g. OAuth tokens).
func safeUpstreamURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	if idx := strings.IndexByte(rawURL, '#'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	return rawURL
}
