//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetMyAccountQuotas_NoAuth_Returns401(t *testing.T) {
	// 未注入 AuthSubject 时返回 401，且不触达任何 service 依赖。
	gin.SetMode(gin.TestMode)
	h := &UserHandler{} // nil deps — 401 路径不会调用它们
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/account-quotas", nil)

	h.GetMyAccountQuotas(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetMyAccountQuotas_FeatureDisabled_Returns200Empty(t *testing.T) {
	// settingService 为 nil → fail-closed 视为功能关闭，返回 200 + 空数组。
	gin.SetMode(gin.TestMode)
	h := &UserHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/account-quotas", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})

	h.GetMyAccountQuotas(c)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Code int `json:"code"`
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Empty(t, body.Data, "disabled feature must return an empty list")
}

func TestGetMyAccountQuotas_FeatureEnabledButMissingDeps_Returns200Empty(t *testing.T) {
	// 接线异常（开关开启但依赖缺失）时按功能关闭处理，不 500。
	// fake repo 返回 user_account_quota_enabled=true 模拟开关开启。
	gin.SetMode(gin.TestMode)
	h := &UserHandler{settingService: service.NewSettingService(&alwaysEnabledSettingRepo{}, nil)}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/account-quotas", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})

	h.GetMyAccountQuotas(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"data":[]`)
}

// alwaysEnabledSettingRepo 让 GetUserAccountQuotaRuntime 恒读到 "true"。
// 其余方法通过嵌入接口保持可编译（若被调用会 nil-deref panic，便于发现未预期路径）。
type alwaysEnabledSettingRepo struct {
	service.SettingRepository
}

func (r *alwaysEnabledSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = "true"
	}
	return out, nil
}

func TestFilterUserVisibleAccounts_GroupsAndShadows(t *testing.T) {
	allowed := map[int64]struct{}{2: {}}
	accounts := []service.Account{
		{ID: 1, Name: "in-allowed-group", GroupIDs: []int64{2}},
		{ID: 2, Name: "in-other-group", GroupIDs: []int64{9}},
		{ID: 3, Name: "ungrouped", GroupIDs: nil},
		{ID: 4, Name: "shadow", GroupIDs: []int64{2}, ParentAccountID: int64Ptr(1)},
		{ID: 5, Name: "mixed-groups", GroupIDs: []int64{9, 2}},
	}

	visible := filterUserVisibleAccounts(accounts, allowed)

	ids := make([]int64, 0, len(visible))
	for _, acc := range visible {
		ids = append(ids, acc.ID)
	}
	require.ElementsMatch(t, []int64{1, 3, 5}, ids,
		"visible = allowed-group accounts + ungrouped accounts; shadows and foreign groups excluded")
}

func TestVisibleGroupRefs_OnlyAllowedAndSorted(t *testing.T) {
	allowed := map[int64]struct{}{1: {}, 3: {}}
	names := map[int64]string{1: "default", 3: "pro"}

	refs := visibleGroupRefs([]int64{3, 9, 1}, allowed, names)

	require.Len(t, refs, 2)
	require.Equal(t, "default", refs[0].Name, "sorted by name")
	require.Equal(t, "pro", refs[1].Name)
	for _, ref := range refs {
		require.NotEqual(t, int64(9), ref.ID, "foreign group must not leak")
	}
}

func TestUserAccountQuotaDTO_FieldWhitelist(t *testing.T) {
	// 序列化后的用户端 DTO 不得包含凭据/内部备注/费用等敏感字段。
	resetsAt := time.Now().Add(2 * time.Hour).UTC()
	row := userAccountQuota{
		ID:             1,
		Name:           "pro20x",
		Platform:       "openai",
		Status:         "active",
		Schedulable:    true,
		RateLimited:    false,
		Groups:         []userAccountQuotaGroup{{ID: 2, Name: "pro"}},
		Usage:          &userAccountQuotaUsage{FiveHour: &userAccountQuotaWindow{Utilization: 12.5, ResetsAt: &resetsAt, RemainingSeconds: 3600}},
		UsageUpdatedAt: &resetsAt,
	}
	raw, err := json.Marshal(row)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, key := range []string{"credentials", "extra", "notes", "error_message", "proxy", "window_stats", "cost"} {
		_, exists := decoded[key]
		require.Falsef(t, exists, "user DTO must not expose %q", key)
	}
	for _, key := range []string{"id", "name", "platform", "status", "groups", "usage", "usage_unavailable"} {
		require.Containsf(t, decoded, key, "user DTO must expose %q", key)
	}
	require.False(t, strings.Contains(string(raw), "password"), "no credentials leak")
}

func TestToUserAccountQuotaUsage_MapsWindowsAndSkipsNil(t *testing.T) {
	usage := &service.UsageInfo{
		FiveHour: &service.UsageProgress{Utilization: 50},
		SevenDay: nil,
	}
	out := toUserAccountQuotaUsage(usage)
	require.NotNil(t, out.FiveHour)
	require.Equal(t, 50.0, out.FiveHour.Utilization)
	require.Nil(t, out.SevenDay)

	require.Nil(t, toUserAccountQuotaUsage(nil))
}

func int64Ptr(v int64) *int64 { return &v }
