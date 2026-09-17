//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func proxyAttributionAccount(id int64, name, host string, port int) *Account {
	proxyID := id
	return &Account{
		ID:      99,
		Name:    "acct",
		ProxyID: &proxyID,
		Proxy:   &Proxy{ID: id, Name: name, Host: host, Port: port},
	}
}

func customRelayAccount(extra map[string]any) *Account {
	account := proxyAttributionAccount(7, "eu-1", "proxy.example", 8080)
	account.Platform = PlatformAnthropic
	account.Type = AccountTypeOAuth
	account.Extra = extra
	return account
}

func TestUsageLogProxyAttribution_HTTPFamily(t *testing.T) {
	missingProxyBinding := proxyAttributionAccount(7, "eu-1", "proxy.example", 8080)
	missingProxyBinding.Proxy = nil

	zeroIDProxy := proxyAttributionAccount(0, "eu-1", "proxy.example", 8080)

	tests := []struct {
		name     string
		account  *Account
		wantID   *int64
		wantName string
		wantHost string
		wantPort *int
	}{
		{name: "nil account is unknown", account: nil, wantName: opsProxyNameUnknown},
		{name: "no binding is direct", account: &Account{}, wantName: opsProxyNameDirect},
		{
			name:    "binding without hydrated proxy is direct",
			account: missingProxyBinding,
			// Op attribution treats a binding without its hydrated proxy as
			// direct: the transport builds a managed route only when both exist.
			wantName: opsProxyNameDirect,
		},
		{
			name:     "hydrated proxy without durable id is unknown",
			account:  zeroIDProxy,
			wantName: opsProxyNameUnknown,
		},
		{
			name:     "managed proxy is snapshotted with endpoint",
			account:  proxyAttributionAccount(7, "eu-1", "proxy.example", 8080),
			wantID:   ptrInt64(7),
			wantName: "eu-1",
			wantHost: "proxy.example",
			wantPort: ptrInt(8080),
		},
		{
			name:     "unnamed proxy falls back to placeholder name",
			account:  proxyAttributionAccount(7, "   ", "proxy.example", 8080),
			wantID:   ptrInt64(7),
			wantName: opsProxyNameUnnamed,
			wantHost: "proxy.example",
			wantPort: ptrInt(8080),
		},
		{
			name: "custom base URL relay is unknown",
			account: customRelayAccount(map[string]any{
				"custom_base_url_enabled": true,
				"custom_base_url":         "https://relay.example",
			}),
			wantName: opsProxyNameUnknown,
		},
		{
			name: "custom base URL flag without URL still uses the proxy",
			account: customRelayAccount(map[string]any{
				"custom_base_url_enabled": true,
				"custom_base_url":         "",
			}),
			wantID:   ptrInt64(7),
			wantName: "eu-1",
			wantHost: "proxy.example",
			wantPort: ptrInt(8080),
		},
		{
			name: "custom base URL set without the flag still uses the proxy",
			account: customRelayAccount(map[string]any{
				"custom_base_url": "https://relay.example",
			}),
			wantID:   ptrInt64(7),
			wantName: "eu-1",
			wantHost: "proxy.example",
			wantPort: ptrInt(8080),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, name, host, port := usageLogProxyAttribution(tt.account)
			require.Equal(t, tt.wantID, id)
			require.Equal(t, tt.wantName, name)
			require.Equal(t, tt.wantHost, host)
			require.Equal(t, tt.wantPort, port)
			if id == nil {
				require.Contains(t, []string{opsProxyNameDirect, opsProxyNameUnknown}, name,
					"a null proxy id must be paired with a sentinel name")
				require.Empty(t, host, "host must stay blank when the route is not a managed proxy")
				require.Nil(t, port, "port must stay blank when the route is not a managed proxy")
			}
		})
	}
}

func TestUsageLogWSProxyAttribution_NoManagedProxyIsUnknown(t *testing.T) {
	id, name, host, port := usageLogWSProxyAttribution(&Account{})
	require.Nil(t, id)
	require.Equal(t, opsProxyNameUnknown, name,
		"a WS request without a managed proxy falls back to the default client, so it is unknown, not direct")
	require.Empty(t, host)
	require.Nil(t, port)

	id, name, host, port = usageLogWSProxyAttribution(proxyAttributionAccount(7, "eu-1", "proxy.example", 8080))
	require.Equal(t, ptrInt64(7), id)
	require.Equal(t, "eu-1", name)
	require.Equal(t, "proxy.example", host)
	require.Equal(t, ptrInt(8080), port)
}

func TestUsageLogProxyAttribution_EndpointNeverLeaksCredentials(t *testing.T) {
	account := proxyAttributionAccount(7, "eu-1", "proxy.example", 8080)
	account.Proxy.Username = "user"
	account.Proxy.Password = "secret"
	account.Proxy.Protocol = "socks5"

	_, _, host, port := usageLogProxyAttribution(account)
	require.Equal(t, "proxy.example", host)
	require.Equal(t, ptrInt(8080), port)
	require.NotContains(t, host, "user")
	require.NotContains(t, host, "socks5")
}
