package service

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamTimingRecordsRawAndParsedMilestones(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	startTime := time.Now().Add(-20 * time.Millisecond)
	resetOpenAIStreamTiming(c)

	body := observeOpenAIUpstreamBody(c, io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")), startTime)
	_, err := io.ReadAll(body)
	require.NoError(t, err)

	recordOpenAIStreamDataTiming(c, startTime, `{"type":"response.output_text.delta","delta":"ok"}`, "response.output_text.delta")
	recordOpenAIStreamEventComplete(c, startTime, "response.output_text.delta")

	firstReadMs, ok := GetOpsLatencyMs(c, OpsUpstreamFirstReadMsKey)
	require.True(t, ok)
	require.GreaterOrEqual(t, firstReadMs, int64(0))

	breakdown := CaptureUsageTimingBreakdown(c, 30)
	require.NotNil(t, breakdown)
	require.NotNil(t, breakdown.UpstreamFirstEventMs)
	require.NotNil(t, breakdown.SemanticFirstTokenMs)
	require.NotNil(t, breakdown.VisibleFirstTokenMs)
	require.Equal(t, "response.output_text.delta", breakdown.UpstreamFirstEventType)
	require.Equal(t, "response.output_text.delta", breakdown.SemanticFirstEventType)
	require.Equal(t, "response.output_text.delta", breakdown.VisibleFirstEventType)
}
