package handler

import (
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// 用户端「账号额度」页面（GET /user/account-quotas）。
//
// 只读展示当前用户可见的上游账号池剩余额度窗口（5 小时 / 7 天等），
// 供普通用户了解可用额度，不需要管理员权限。设计约束：
//   - 功能开关 user_account_quota_enabled 默认关闭（opt-in），关闭时返回空数组；
//   - 只返回用户可访问分组下的账号 + 未绑定分组的公共账号，影子账号不展示；
//   - 字段白名单：不暴露 credentials / extra / notes / error_message / 代理等内部信息，
//     也不暴露窗口统计里的费用（cost）等敏感计费数据。

// userAccountQuotaWindow 单个额度窗口的用户端只读视图。
type userAccountQuotaWindow struct {
	Utilization      float64    `json:"utilization"`       // 已用百分比（0-100）
	ResetsAt         *time.Time `json:"resets_at"`         // 重置时间
	RemainingSeconds int        `json:"remaining_seconds"` // 距重置的剩余秒数
}

// userAccountQuotaUsage 账号用量窗口集合（仅含用户端展示需要的窗口）。
type userAccountQuotaUsage struct {
	FiveHour          *userAccountQuotaWindow `json:"five_hour,omitempty"`
	SevenDay          *userAccountQuotaWindow `json:"seven_day,omitempty"`
	SevenDaySonnet    *userAccountQuotaWindow `json:"seven_day_sonnet,omitempty"`
	SevenDayFable     *userAccountQuotaWindow `json:"seven_day_fable,omitempty"`
	ThirtyDay         *userAccountQuotaWindow `json:"thirty_day,omitempty"`
	GeminiSharedDaily *userAccountQuotaWindow `json:"gemini_shared_daily,omitempty"`
	GeminiProDaily    *userAccountQuotaWindow `json:"gemini_pro_daily,omitempty"`
	GeminiFlashDaily  *userAccountQuotaWindow `json:"gemini_flash_daily,omitempty"`
}

// userAccountQuotaGroup 账号关联的、用户可见的分组摘要。
type userAccountQuotaGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// userAccountQuota 用户端「账号额度」单行 DTO（字段白名单）。
type userAccountQuota struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
	// Schedulable 标记账号是否参与调度（管理员暂停时不参与）。
	Schedulable bool `json:"schedulable"`
	// RateLimited 标记账号当前是否处于限流状态。
	RateLimited bool                    `json:"rate_limited"`
	Groups      []userAccountQuotaGroup `json:"groups"`
	// Usage 为 nil 且 UsageUnavailable 为 true 时，前端展示「暂无额度数据」。
	Usage            *userAccountQuotaUsage `json:"usage,omitempty"`
	UsageUnavailable bool                   `json:"usage_unavailable"`
	UsageUpdatedAt   *time.Time             `json:"usage_updated_at,omitempty"`
}

// userAccountQuotaEnabled 返回当前用户端「账号额度」功能是否启用。
// settingService 为 nil（测试场景）视为关闭（fail-closed，与 opt-in 默认一致）。
func (h *UserHandler) userAccountQuotaEnabled(c *gin.Context) bool {
	if h.settingService == nil {
		return false
	}
	return h.settingService.GetUserAccountQuotaRuntime(c.Request.Context()).Enabled
}

// GetMyAccountQuotas GET /user/account-quotas
// 返回当前用户可见的上游账号及其剩余额度窗口（只读）。
// 功能开关未启用时返回空数组（不暴露账号信息），行为与「可用渠道」一致：
// 未登录先 401，登录后再按开关决定。
func (h *UserHandler) GetMyAccountQuotas(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if !h.userAccountQuotaEnabled(c) {
		response.Success(c, []userAccountQuota{})
		return
	}
	// 依赖缺失（仅测试/接线异常时发生）按功能关闭处理，避免 500。
	if h.apiKeyService == nil || h.accountRepo == nil || h.accountUsageService == nil {
		response.Success(c, []userAccountQuota{})
		return
	}

	userGroups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	allowedGroupIDs := make(map[int64]struct{}, len(userGroups))
	groupNames := make(map[int64]string, len(userGroups))
	for i := range userGroups {
		allowedGroupIDs[userGroups[i].ID] = struct{}{}
		groupNames[userGroups[i].ID] = userGroups[i].Name
	}

	accounts, err := h.accountRepo.ListActive(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	visible := filterUserVisibleAccounts(accounts, allowedGroupIDs)
	if len(visible) == 0 {
		response.Success(c, []userAccountQuota{})
		return
	}

	accountIDs := make([]int64, 0, len(visible))
	for i := range visible {
		accountIDs = append(accountIDs, visible[i].ID)
	}
	// force=false：走管理端同一套缓存/节流逻辑，用户页面不主动触发上游探测风暴。
	usageByID, usageErrors, err := h.accountUsageService.GetUsageBatch(c.Request.Context(), accountIDs, false)
	if err != nil {
		// 整批失败（如数据库错误）视为接口错误；单账号失败只影响该行。
		response.ErrorFrom(c, err)
		return
	}

	out := make([]userAccountQuota, 0, len(visible))
	for i := range visible {
		acc := &visible[i]
		row := userAccountQuota{
			ID:          acc.ID,
			Name:        acc.Name,
			Platform:    acc.Platform,
			Status:      acc.Status,
			Schedulable: acc.Schedulable,
			RateLimited: acc.IsRateLimited(),
			Groups:      visibleGroupRefs(acc.GroupIDs, allowedGroupIDs, groupNames),
		}
		usage := usageByID[acc.ID]
		_, hasUsageErr := usageErrors[acc.ID]
		if hasUsageErr || usage == nil {
			row.UsageUnavailable = true
		} else {
			row.Usage = toUserAccountQuotaUsage(usage)
			row.UsageUpdatedAt = usage.UpdatedAt
		}
		out = append(out, row)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return out[i].ID < out[j].ID
	})

	response.Success(c, out)
}

// filterUserVisibleAccounts 返回用户可见的账号：
//   - 绑定在用户可访问分组（公开分组 / 已授权专属分组 / 有效订阅分组）下的账号；或
//   - 未绑定任何分组的公共兜底账号（对所有用户可用）。
//
// 影子账号（ParentAccountID 非空，不持凭据、镜像母账号）不展示，
// 避免与母账号重复占用同一份额度造成误解。
func filterUserVisibleAccounts(accounts []service.Account, allowedGroupIDs map[int64]struct{}) []service.Account {
	out := make([]service.Account, 0, len(accounts))
	for i := range accounts {
		acc := &accounts[i]
		if acc.IsShadow() {
			continue
		}
		if len(acc.GroupIDs) == 0 {
			out = append(out, *acc)
			continue
		}
		for _, gid := range acc.GroupIDs {
			if _, ok := allowedGroupIDs[gid]; ok {
				out = append(out, *acc)
				break
			}
		}
	}
	return out
}

// visibleGroupRefs 把账号的分组 ID 列表收敛为用户可见分组的 {id,name} 摘要。
// 不在 allowedGroupIDs 中的分组（其他用户的专属分组）不会下发。
func visibleGroupRefs(groupIDs []int64, allowedGroupIDs map[int64]struct{}, groupNames map[int64]string) []userAccountQuotaGroup {
	out := make([]userAccountQuotaGroup, 0, len(groupIDs))
	for _, gid := range groupIDs {
		if _, ok := allowedGroupIDs[gid]; !ok {
			continue
		}
		name := groupNames[gid]
		if name == "" {
			name = "#"
		}
		out = append(out, userAccountQuotaGroup{ID: gid, Name: name})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// toUserAccountQuotaUsage 把 service.UsageInfo 收敛为用户端窗口白名单。
// 窗口统计（window_stats：费用/请求数）等内部数据一律不下发。
func toUserAccountQuotaUsage(u *service.UsageInfo) *userAccountQuotaUsage {
	if u == nil {
		return nil
	}
	return &userAccountQuotaUsage{
		FiveHour:          toUserAccountQuotaWindow(u.FiveHour),
		SevenDay:          toUserAccountQuotaWindow(u.SevenDay),
		SevenDaySonnet:    toUserAccountQuotaWindow(u.SevenDaySonnet),
		SevenDayFable:     toUserAccountQuotaWindow(u.SevenDayFable),
		ThirtyDay:         toUserAccountQuotaWindow(u.ThirtyDay),
		GeminiSharedDaily: toUserAccountQuotaWindow(u.GeminiSharedDaily),
		GeminiProDaily:    toUserAccountQuotaWindow(u.GeminiProDaily),
		GeminiFlashDaily:  toUserAccountQuotaWindow(u.GeminiFlashDaily),
	}
}

// toUserAccountQuotaWindow 收敛单个窗口为用户端视图。
func toUserAccountQuotaWindow(w *service.UsageProgress) *userAccountQuotaWindow {
	if w == nil {
		return nil
	}
	return &userAccountQuotaWindow{
		Utilization:      w.Utilization,
		ResetsAt:         w.ResetsAt,
		RemainingSeconds: w.RemainingSeconds,
	}
}
