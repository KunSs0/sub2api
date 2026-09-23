package service

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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
		OpsOpenAISemanticFirstTokenMsKey,
		OpsOpenAIVisibleFirstTokenMsKey,
	} {
		c.Set(key, nil)
	}
	for _, key := range []string{
		OpsUpstreamFirstEventTypeKey,
		OpsOpenAISemanticFirstEventTypeKey,
		OpsOpenAIVisibleFirstEventTypeKey,
	} {
		c.Set(key, "")
	}
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
	if openAIStreamDataStartsSemanticTTFT(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAISemanticFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAISemanticFirstEventTypeKey, eventType)
	}
	if openAIStreamDataStartsVisibleOutput(data, eventType) {
		recordOpenAIStreamLatencyOnce(c, OpsOpenAIVisibleFirstTokenMsKey, startTime)
		recordOpenAIStreamStringOnce(c, OpsOpenAIVisibleFirstEventTypeKey, eventType)
	}
}

func recordOpenAIStreamEventComplete(c *gin.Context, startTime time.Time, eventType string) {
	if c == nil {
		return
	}
	recordOpenAIStreamLatencyOnce(c, OpsUpstreamFirstEventMsKey, startTime)
	recordOpenAIStreamStringOnce(c, OpsUpstreamFirstEventTypeKey, eventType)
}
