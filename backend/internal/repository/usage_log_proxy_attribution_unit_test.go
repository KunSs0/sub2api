//go:build unit

package repository

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newProxyAttributionUsageLog(proxyID *int64, name, host *string, port *int) *service.UsageLog {
	return &service.UsageLog{
		UserID:    1,
		APIKeyID:  2,
		AccountID: 3,
		RequestID: "req-proxy-attribution",
		Model:     "claude-3",
		ProxyID:   proxyID,
		ProxyName: name,
		ProxyHost: host,
		ProxyPort: port,
		CreatedAt: time.Now().UTC(),
	}
}

// TestPrepareUsageLogInsert_ProxyAttributionArgWiring pins the four proxy
// snapshot columns to the arg slice / arg-type table. They sit between
// native_compaction_v2 and created_at, so the offsets below are counted from
// the tail: created_at is last, then proxy_port, proxy_host, proxy_name,
// proxy_id, native_compaction_v2.
func TestPrepareUsageLogInsert_ProxyAttributionArgWiring(t *testing.T) {
	proxyID := int64(7)
	name := "eu-1"
	host := "proxy.example"
	port := 8080
	prepared := prepareUsageLogInsert(newProxyAttributionUsageLog(&proxyID, &name, &host, &port))

	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	argCount := len(prepared.args)

	idArg, ok := prepared.args[argCount-5].(sql.NullInt64)
	require.True(t, ok, "proxy_id arg should be sql.NullInt64, got %T", prepared.args[argCount-5])
	require.True(t, idArg.Valid)
	require.Equal(t, proxyID, idArg.Int64)
	require.Equal(t, "bigint", usageLogInsertArgTypes[argCount-5])

	nameArg, ok := prepared.args[argCount-4].(sql.NullString)
	require.True(t, ok, "proxy_name arg should be sql.NullString, got %T", prepared.args[argCount-4])
	require.True(t, nameArg.Valid)
	require.Equal(t, name, nameArg.String)
	require.Equal(t, "text", usageLogInsertArgTypes[argCount-4])

	hostArg, ok := prepared.args[argCount-3].(sql.NullString)
	require.True(t, ok, "proxy_host arg should be sql.NullString, got %T", prepared.args[argCount-3])
	require.True(t, hostArg.Valid)
	require.Equal(t, host, hostArg.String)
	require.Equal(t, "text", usageLogInsertArgTypes[argCount-3])

	portArg, ok := prepared.args[argCount-2].(sql.NullInt64)
	require.True(t, ok, "proxy_port arg should be sql.NullInt64, got %T", prepared.args[argCount-2])
	require.True(t, portArg.Valid)
	require.Equal(t, int64(port), portArg.Int64)
	require.Equal(t, "integer", usageLogInsertArgTypes[argCount-2])

	// The attribution must never be persisted as an empty string.
	absent := prepareUsageLogInsert(newProxyAttributionUsageLog(nil, nil, nil, nil))
	require.False(t, absent.args[argCount-5].(sql.NullInt64).Valid, "absent proxy id must be NULL")
	require.False(t, absent.args[argCount-4].(sql.NullString).Valid, "absent proxy name must be NULL")
	require.False(t, absent.args[argCount-3].(sql.NullString).Valid, "absent proxy host must be NULL")
	require.False(t, absent.args[argCount-2].(sql.NullInt64).Valid, "absent proxy port must be NULL")

	empty := ""
	emptyName := prepareUsageLogInsert(newProxyAttributionUsageLog(nil, &empty, &empty, nil))
	require.False(t, emptyName.args[argCount-4].(sql.NullString).Valid, "empty proxy name must be NULL")
	require.False(t, emptyName.args[argCount-3].(sql.NullString).Valid, "empty proxy host must be NULL")
}

// TestUsageLogInsertArgTypes_TailOrder fails loudly the next time a column is
// appended without updating the offset-based tests.
func TestUsageLogInsertArgTypes_TailOrder(t *testing.T) {
	tail := usageLogInsertArgTypes[len(usageLogInsertArgTypes)-8:]
	require.Equal(t, []string{
		"text",        // upstream_request_id
		"text",        // session_id
		"boolean",     // native_compaction_v2
		"bigint",      // proxy_id
		"text",        // proxy_name
		"text",        // proxy_host
		"integer",     // proxy_port
		"timestamptz", // created_at
	}, tail)
}

// TestUsageLogInsertQueries_IncludeProxyAttribution guards that the SELECT list
// and every generated INSERT path reference the proxy snapshot columns.
func TestUsageLogInsertQueries_IncludeProxyAttribution(t *testing.T) {
	for _, column := range []string{"proxy_id", "proxy_name", "proxy_host", "proxy_port"} {
		require.Contains(t, usageLogSelectColumns, column,
			"SELECT column list must include %s", column)
	}

	proxyID := int64(7)
	name := "eu-1"
	host := "proxy.example"
	port := 8080
	log := newProxyAttributionUsageLog(&proxyID, &name, &host, &port)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key},
		map[string]usageLogInsertPrepared{key: prepared})
	require.Len(t, batchArgs, len(prepared.args)+1,
		"batch args include the synthetic input_index before usage-log values")

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Len(t, bestEffortArgs, len(prepared.args))

	for _, column := range []string{"proxy_id", "proxy_name", "proxy_host", "proxy_port"} {
		// Two column references (INSERT column list + SELECT ... FROM input) plus the CTE definition.
		require.GreaterOrEqual(t, strings.Count(batchQuery, column), 3,
			"batch INSERT must reference %s in the CTE, the column list and the select", column)
		require.Contains(t, bestEffortQuery, column,
			"best-effort INSERT must reference %s", column)
	}
}
