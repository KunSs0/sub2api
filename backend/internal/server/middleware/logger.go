package middleware

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 请求日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		startTime := time.Now()

		// 请求路径
		path := c.Request.URL.Path

		// 处理请求
		c.Next()

		// 跳过健康检查等高频探针路径的日志
		if path == "/health" || path == "/setup/status" {
			return
		}

		endTime := time.Now()
		latency := endTime.Sub(startTime)

		method := c.Request.Method
		statusCode := c.Writer.Status()
		clientIP := ip.GetClientIP(c)
		protocol := c.Request.Proto
		accountID, hasAccountID := c.Request.Context().Value(ctxkey.AccountID).(int64)
		platform, _ := c.Request.Context().Value(ctxkey.Platform).(string)
		model, _ := c.Request.Context().Value(ctxkey.Model).(string)
		reason, rejected := GetIngressRejectReason(c)
		if rejected {
			recordIngressReject(c, reason)
			allowed, droppedSummary := globalIngressRejectAccessSampler.allow(endTime)
			if droppedSummary > 0 {
				logger.FromContext(c.Request.Context()).Info("ingress rejection access logs dropped",
					zap.String("component", "http.access"),
					zap.Uint64("dropped_count", droppedSummary),
					zap.Bool(logger.OpsSystemLogSkipField, true),
				)
			}
			if !allowed {
				return
			}
		}

		fields := []zap.Field{
			zap.String("component", "http.access"),
			zap.Int("status_code", statusCode),
			zap.Int64("latency_ms", latency.Milliseconds()),
			zap.String("client_ip", clientIP),
			zap.String("protocol", protocol),
			zap.String("method", method),
			zap.String("path", path),
		}
		if rejected {
			fields = append(fields,
				zap.String("ingress_reject_reason", string(reason)),
				zap.Bool(logger.OpsSystemLogSkipField, true),
			)
		}
		if hasAccountID && accountID > 0 {
			fields = append(fields, zap.Int64("account_id", accountID))
		}
		if platform != "" {
			fields = append(fields, zap.String("platform", platform))
		}
		if model != "" {
			fields = append(fields, zap.String("model", model))
		}
		fields = appendOpsTimingFields(c, fields)

		l := logger.FromContext(c.Request.Context()).With(fields...)
		l.Info("http request completed", zap.Time("completed_at", endTime))

		if len(c.Errors) > 0 {
			l.Warn("http request contains gin errors", zap.String("errors", c.Errors.String()))
		}
	}
}

// appendOpsTimingFields exposes the timing context on successful access logs.
// The existing latency fields are intentionally kept unchanged; these extra
// fields make the request path diagnosable without requiring an ops error row.
//
// For a normal OpenAI stream, the forward path can be decomposed as:
//
//	dispatch offset + upstream header wait + wait after headers + after TTFT
//
// The first two values are measured directly. The latter two are derived from
// the semantic TTFT and the forward duration.
func appendOpsTimingFields(c *gin.Context, fields []zap.Field) []zap.Field {
	appendLatency := func(name, key string) (int64, bool) {
		value, ok := service.GetOpsLatencyMs(c, key)
		if ok {
			fields = append(fields, zap.Int64(name, value))
		}
		return value, ok
	}

	appendLatency("auth_latency_ms", service.OpsAuthLatencyMsKey)
	appendLatency("routing_latency_ms", service.OpsRoutingLatencyMsKey)
	upstreamHeaderMs, hasUpstreamHeader := appendLatency("upstream_header_latency_ms", service.OpsUpstreamLatencyMsKey)
	responseMs, hasResponse := appendLatency("response_latency_ms", service.OpsResponseLatencyMsKey)
	ttftMs, hasTTFT := appendLatency("first_token_ms", service.OpsTimeToFirstTokenMsKey)
	dispatchOffsetMs, hasDispatchOffset := appendLatency("upstream_dispatch_offset_ms", service.OpsUpstreamDispatchOffsetMsKey)

	// response_latency_ms is the forward duration with the HTTP header wait
	// removed. When the upstream latency is unavailable, it already represents
	// the complete forward duration.
	forwardMs := int64(0)
	hasForward := false
	if hasResponse {
		forwardMs = responseMs
		hasForward = true
		if hasUpstreamHeader {
			forwardMs += upstreamHeaderMs
		}
		fields = append(fields, zap.Int64("forward_latency_ms", forwardMs))
	}

	if hasTTFT && hasForward && forwardMs >= ttftMs {
		fields = append(fields, zap.Int64("after_first_token_ms", forwardMs-ttftMs))
	}
	if hasTTFT && hasDispatchOffset && hasUpstreamHeader {
		waitAfterHeadersMs := ttftMs - dispatchOffsetMs - upstreamHeaderMs
		if waitAfterHeadersMs >= 0 {
			fields = append(fields, zap.Int64("upstream_wait_after_headers_ms", waitAfterHeadersMs))
		}
	}
	return fields
}
