/**
 * User-facing Account Quota API endpoints
 * Read-only view of upstream account-pool remaining quota windows (账号额度).
 */

import { apiClient } from './client'

/** 单个额度窗口：utilization 为已用百分比（0-100）。 */
export interface AccountQuotaWindow {
  utilization: number
  resets_at: string | null
  remaining_seconds: number
}

/** 后端下发的窗口白名单（其余内部窗口不下发）。 */
export interface AccountQuotaUsage {
  five_hour?: AccountQuotaWindow | null
  seven_day?: AccountQuotaWindow | null
  seven_day_sonnet?: AccountQuotaWindow | null
  seven_day_fable?: AccountQuotaWindow | null
  thirty_day?: AccountQuotaWindow | null
  gemini_shared_daily?: AccountQuotaWindow | null
  gemini_pro_daily?: AccountQuotaWindow | null
  gemini_flash_daily?: AccountQuotaWindow | null
}

export interface AccountQuotaGroup {
  id: number
  name: string
}

/** 用户端账号额度行（后端字段白名单，不含凭据/备注/费用）。 */
export interface UserAccountQuota {
  id: number
  name: string
  platform: string
  status: string
  schedulable: boolean
  rate_limited: boolean
  groups: AccountQuotaGroup[]
  usage?: AccountQuotaUsage | null
  usage_unavailable: boolean
  usage_updated_at?: string | null
}

export type UserAccountQuotaList = UserAccountQuota[]

/**
 * 拉取当前用户可见账号的剩余额度。
 * 功能开关关闭时后端返回空数组（200）。
 */
export async function list(options?: { signal?: AbortSignal }): Promise<UserAccountQuotaList> {
  const { data } = await apiClient.get<UserAccountQuotaList>('/user/account-quotas', {
    signal: options?.signal,
  })
  return data ?? []
}

export const userAccountQuotaAPI = {
  list,
}

export default userAccountQuotaAPI
