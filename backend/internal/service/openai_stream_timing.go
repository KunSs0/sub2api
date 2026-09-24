package service

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// openAIUpstreamTimingReadCloser records the first non-empty read from the
// upstream response body. This sits below the SSE scanner, so it tells us when
// bytes first crossed the response-body boundary before line/event parsing.
type openAIUpstreamTimingReadCloser struct {
	io.ReadCloser
	c         *gin.Context
	startTime time.Time
	once      sync.Once
}

func (r *openAIUpstreamTimingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.once.Do(func() {
			SetOpsLatencyMs(r.c, OpsUpstreamFirstReadMsKey, openAIStreamTimingElapsedMs(r.startTime))
		})
	}
	return n, err
}

func (r *openAIUpstreamTimingReadCloser) Close() error {
	return r.ReadCloser.Close()
}

func observeOpenAIUpstreamBody(c *gin.Context, body io.ReadCloser, startTime time.Time) io.ReadCloser {
	if body == nil {
		return nil
	}
	if _, alreadyObserved := body.(*openAIUpstreamTimingReadCloser); alreadyObserved {
		return body
	}
	return &openAIUpstreamTimingReadCloser{ReadCloser: body, c: c, startTime: startTime}
}

func openAIStreamTimingElapsedMs(startTime time.Time) int64 {
	if startTime.IsZero() {
		return 0
	}
	value := time.Since(startTime).Milliseconds()
	if value < 0 {
		return 0
	}
	return value
}

func resetOpenAIStreamTiming(c *gin.Context) {
	if c == nil {
		return
	}
	for _, key := range []string{
		OpsUpstreamFirstReadMsKey,
		OpsUpstreamFirstEventMsKey,
		OpsOpenAIOutputItemFirstMsKey,
		OpsOpenAIInProgressMsKey,
		OpsOpenAIOutputItemWaitMsKey,
		OpsOpenAISemanticFirstTokenMsKey,
		OpsOpenAIVisibleFirstTokenMsKey,
		OpsOpenAIAnswerFirstTokenMsKey,
		OpsOpenAIReasoningFirstTokenMsKey,
		OpsOpenAIToolFirstTokenMsKey,
		OpsUpstreamCompletedMsKey,
		OpsUpstreamEventCountKey,
		OpsUpstreamMaxEventGapMsKey,
	} {
		c.Set(key, nil)
	}
	for _, key := range []string{
		OpsUpstreamFirstEventTypeKey,
		OpsOpenAIOutputItemFirstEventTypeKey,
		OpsOpenAIOutputItemFirstTypeKey,
		OpsOpenAISemanticFirstEventTypeKey,
		OpsOpenAIVisibleFirstEventTypeKey,
		OpsOpenAIAnswerFirstEventTypeKey,
		OpsOpenAIReasoningFirstEventTypeKey,
		OpsOpenAIToolFirstEventTypeKey,
		OpsUpstreamMaxEventGapFromTypeKey,
		OpsUpstreamMaxEventGapToTypeKey,
		OpsOpenAIStreamLastEventTypeKey,
	} {
		c.Set(key, "")
	}
	c.Set(OpsOpenAIStreamLastEventAtKey, time.Time{})
}

func recordOpenAIStreamLatencyOnce(c *gin.Context, key string, startTime time.Time) {
	if c == nil {
		return
	}
	if _, exists := GetOpsLatencyMs(c, key); exists {
		return
	}
	SetOpsLatencyMs(c, key, openAIStreamTimingElapsedMs(startTime))
}

func recordOpenAIStreamStringOnce(c *gin.Context, key, value string) {
	value = strings.TrimSpace(value)
	if c == nil || value == "" {
		return
	}
	if _, exists := GetOpsString(c, key); exists {
		return
	}
	SetOpsString(c, key, value)
}

func recordOpenAIStreamDataTiming(c *gin.Context, startTime time.Time, data, eventType string) {
	if c == nil {
		return
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "response.in_progress" {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIInProgressMsKey, startTime)
	}
	if openAIStreamDataStartsOutputItem(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIOutputItemFirstMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAIOutputItemFirstEventTypeKey, eventType)
		recordOpenAIStreamStringOnce(c, OpsOpenAIOutputItemFirstTypeKey, gjson.Get(strings.TrimSpace(data), "item.type").String())
		if outputItemMs, ok := GetOpsLatencyMs(c, OpsOpenAIOutputItemFirstMsKey); ok {
			if inProgressMs, ok := GetOpsLatencyMs(c, OpsOpenAIInProgressMsKey); ok && outputItemMs >= inProgressMs {
				SetOpsLatencyMs(c, OpsOpenAIOutputItemWaitMsKey, outputItemMs-inProgressMs)
			}
		}
	}
	if openAIStreamDataStartsSemanticTTFT(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAISemanticFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAISemanticFirstEventTypeKey, eventType)
	}
	if openAIStreamDataStartsVisibleOutput(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIVisibleFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAIVisibleFirstEventTypeKey, eventType)
	}
	if openAIStreamDataStartsAnswerOutput(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIAnswerFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAIAnswerFirstEventTypeKey, eventType)
	}
	if openAIStreamDataStartsReasoningOutput(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIReasoningFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAIReasoningFirstEventTypeKey, eventType)
	}
	if openAIStreamDataStartsToolOutput(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIToolFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAIToolFirstEventTypeKey, eventType)
	}
}

func recordOpenAIStreamEventComplete(c *gin.Context, startTime time.Time, eventType string) {
	if c == nil {
		return
	}
	recordOpenAIStreamLatencyOnce(c, OpsUpstreamFirstEventMsKey, startTime)
	recordOpenAIStreamStringOnce(c, OpsUpstreamFirstEventTypeKey, eventType)

	now := time.Now()
	if previous, ok := c.Get(OpsOpenAIStreamLastEventAtKey); ok {
		if previousAt, ok := previous.(time.Time); ok && !previousAt.IsZero() {
			gapMs := now.Sub(previousAt).Milliseconds()
			if gapMs >= 0 {
				maxGap, hasMaxGap := GetOpsLatencyMs(c, OpsUpstreamMaxEventGapMsKey)
				if !hasMaxGap || gapMs > maxGap {
					SetOpsLatencyMs(c, OpsUpstreamMaxEventGapMsKey, gapMs)
					fromType, _ := GetOpsString(c, OpsOpenAIStreamLastEventTypeKey)
					SetOpsString(c, OpsUpstreamMaxEventGapFromTypeKey, fromType)
					SetOpsString(c, OpsUpstreamMaxEventGapToTypeKey, eventType)
				}
			}
		}
	}
	c.Set(OpsOpenAIStreamLastEventAtKey, now)
	SetOpsString(c, OpsOpenAIStreamLastEventTypeKey, eventType)

	eventCount, _ := GetOpsLatencyMs(c, OpsUpstreamEventCountKey)
	SetOpsLatencyMs(c, OpsUpstreamEventCountKey, eventCount+1)
	if eventType == "response.completed" || eventType == "response.done" || eventType == "[DONE]" {
		recordOpenAIStreamLatencyOnce(c, OpsUpstreamCompletedMsKey, startTime)
	}
}

func openAIStreamDataStartsOutputItem(data, eventType string) bool {
	return strings.TrimSpace(eventType) == "response.output_item.added" && gjson.Valid(strings.TrimSpace(data))
}

func openAIStreamDataStartsAnswerOutput(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || !gjson.Valid(trimmed) {
		return false
	}
	switch strings.TrimSpace(eventType) {
	case "response.output_text.delta", "response.refusal.delta", "response.audio_transcript.delta":
		return gjson.Get(trimmed, "delta").String() != ""
	case "response.output_text.done", "response.refusal.done", "response.audio_transcript.done":
		return gjson.Get(trimmed, "text").String() != "" || gjson.Get(trimmed, "refusal").String() != ""
	case "response.content_part.added", "response.content_part.done":
		part := gjson.Get(trimmed, "part")
		return part.Get("type").String() == "output_text" && part.Get("text").String() != ""
	default:
		return false
	}
}

func openAIStreamDataStartsReasoningOutput(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || !gjson.Valid(trimmed) {
		return false
	}
	switch strings.TrimSpace(eventType) {
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return gjson.Get(trimmed, "delta").String() != ""
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
		return gjson.Get(trimmed, "text").String() != ""
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		part := gjson.Get(trimmed, "part")
		return part.Get("type").String() == "summary_text" && part.Get("text").String() != ""
	default:
		return false
	}
}

func openAIStreamDataStartsToolOutput(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || !gjson.Valid(trimmed) {
		return false
	}
	switch strings.TrimSpace(eventType) {
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		return gjson.Get(trimmed, "delta").String() != ""
	case "response.function_call_arguments.done":
		return gjson.Get(trimmed, "arguments").String() != ""
	case "response.custom_tool_call_input.done":
		return gjson.Get(trimmed, "input").String() != ""
	default:
		return false
	}
}
