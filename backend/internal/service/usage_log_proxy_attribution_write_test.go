//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func managedProxyAccount(id int64) *Account {
	proxyID := id
	return &Account{
		ID:      701,
		ProxyID: &proxyID,
		Proxy:   &Proxy{ID: id, Name: "eu-1", Host: "proxy.example", Port: 8080},
	}
}

func TestGatewayServiceRecordUsage_SnapshotsManagedProxyAttribution(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_proxy_attribution",
			Usage:     ClaudeUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "claude-sonnet-4",
			Duration:  time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: managedProxyAccount(42),
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ProxyID)
	require.Equal(t, int64(42), *usageRepo.lastLog.ProxyID)
	require.NotNil(t, usageRepo.lastLog.ProxyName)
	require.Equal(t, "eu-1", *usageRepo.lastLog.ProxyName)
	require.NotNil(t, usageRepo.lastLog.ProxyHost)
	require.Equal(t, "proxy.example", *usageRepo.lastLog.ProxyHost)
	require.NotNil(t, usageRepo.lastLog.ProxyPort)
	require.Equal(t, 8080, *usageRepo.lastLog.ProxyPort)
}

func TestGatewayServiceRecordUsage_DirectRouteIsMarkedDirect(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_proxy_direct",
			Usage:     ClaudeUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "claude-sonnet-4",
			Duration:  time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ProxyID)
	require.NotNil(t, usageRepo.lastLog.ProxyName)
	require.Equal(t, opsProxyNameDirect, *usageRepo.lastLog.ProxyName)
	require.Nil(t, usageRepo.lastLog.ProxyHost)
	require.Nil(t, usageRepo.lastLog.ProxyPort)
}

func TestGatewayServiceRecordUsage_CustomRelayRouteIsUnknown(t *testing.T) {
	account := managedProxyAccount(42)
	account.Platform = PlatformAnthropic
	account.Type = AccountTypeOAuth
	account.Extra = map[string]any{
		"custom_base_url_enabled": true,
		"custom_base_url":         "https://relay.example",
	}

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_proxy_relay",
			Usage:     ClaudeUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "claude-sonnet-4",
			Duration:  time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: account,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ProxyID)
	require.NotNil(t, usageRepo.lastLog.ProxyName)
	require.Equal(t, opsProxyNameUnknown, *usageRepo.lastLog.ProxyName,
		"a custom base URL relay dials upstream itself, so the egress route is unprovable")
	require.Nil(t, usageRepo.lastLog.ProxyHost)
	require.Nil(t, usageRepo.lastLog.ProxyPort)
}

func TestOpenAIGatewayServiceRecordUsage_SnapshotsManagedProxyAttribution(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_proxy_attribution",
			Usage:     OpenAIUsage{},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:  &APIKey{ID: 1000, Quota: 100, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2000},
		Account: managedProxyAccount(42),
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ProxyID)
	require.Equal(t, int64(42), *usageRepo.lastLog.ProxyID)
	require.NotNil(t, usageRepo.lastLog.ProxyName)
	require.Equal(t, "eu-1", *usageRepo.lastLog.ProxyName)
	require.NotNil(t, usageRepo.lastLog.ProxyPort)
	require.Equal(t, 8080, *usageRepo.lastLog.ProxyPort)
}

func TestOpenAIGatewayServiceRecordUsage_WSWithoutManagedProxyIsUnknown(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:    "resp_proxy_ws",
			OpenAIWSMode: true,
			Usage:        OpenAIUsage{},
			Model:        "gpt-5.1",
			Duration:     time.Second,
		},
		APIKey:  &APIKey{ID: 1000, Quota: 100, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2000},
		Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ProxyID)
	require.NotNil(t, usageRepo.lastLog.ProxyName)
	require.Equal(t, opsProxyNameUnknown, *usageRepo.lastLog.ProxyName,
		"a WS request without a managed proxy may still use the environment proxy, so it is unknown, not direct")
}
