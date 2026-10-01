<template>
  <AppLayout>
    <!-- Page header -->
    <div class="mb-6 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-xl font-bold text-gray-900 dark:text-white">
          {{ t('accountQuota.title') }}
        </h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('accountQuota.description') }}
        </p>
      </div>
      <div class="flex items-center gap-3">
        <span v-if="lastUpdated" class="text-xs text-gray-400 dark:text-gray-500">
          {{ t('accountQuota.lastUpdated', { time: formatTime(lastUpdated) }) }}
        </span>
        <button
          class="btn btn-secondary"
          :disabled="loading"
          :title="t('accountQuota.refresh')"
          @click="load(true)"
        >
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          <span class="ml-1 hidden sm:inline">{{ t('accountQuota.refresh') }}</span>
        </button>
      </div>
    </div>

    <!-- Loading skeleton (first load) -->
    <div v-if="loading && rows.length === 0" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <div v-for="n in 3" :key="n" class="card animate-pulse p-5">
        <div class="h-4 w-1/3 rounded bg-gray-200 dark:bg-dark-700"></div>
        <div class="mt-5 h-3 w-full rounded bg-gray-200 dark:bg-dark-700"></div>
        <div class="mt-4 h-3 w-full rounded bg-gray-200 dark:bg-dark-700"></div>
        <div class="mt-4 h-3 w-2/3 rounded bg-gray-200 dark:bg-dark-700"></div>
      </div>
    </div>

    <!-- Empty state -->
    <div v-else-if="rows.length === 0" class="card p-10 text-center">
      <p class="text-sm font-medium text-gray-900 dark:text-white">
        {{ t('accountQuota.empty.title') }}
      </p>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {{ t('accountQuota.empty.description') }}
      </p>
    </div>

    <!-- Account cards -->
    <div v-else class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <div
        v-for="row in rows"
        :key="row.account.id"
        class="card flex flex-col p-5"
        data-tour="account-quota-card"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold text-gray-900 dark:text-white">
              {{ row.account.name }}
            </p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ platformLabel(row.account.platform) }}
            </p>
          </div>
          <div class="flex flex-shrink-0 flex-wrap items-center justify-end gap-1">
            <span
              v-if="!row.account.schedulable"
              class="rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
            >
              {{ t('accountQuota.paused') }}
            </span>
            <span
              v-else-if="row.account.rate_limited"
              class="rounded-full bg-red-100 px-2 py-0.5 text-[11px] font-medium text-red-700 dark:bg-red-900/30 dark:text-red-300"
            >
              {{ t('accountQuota.rateLimited') }}
            </span>
            <span
              v-else
              class="rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
            >
              {{ t('accountQuota.running') }}
            </span>
          </div>
        </div>

        <p
          v-if="row.account.groups.length > 0"
          class="mt-2 truncate text-xs text-gray-400 dark:text-gray-500"
        >
          {{ t('accountQuota.groups', { names: groupNames(row.account) }) }}
        </p>

        <div class="mt-4 flex-1 space-y-4">
          <template v-if="row.windows.length > 0">
            <div v-for="w in row.windows" :key="w.key">
              <div class="flex items-baseline justify-between gap-2 text-xs">
                <span class="font-medium text-gray-600 dark:text-gray-300">{{ w.label }}</span>
                <span class="font-semibold" :class="w.toneClass">{{ w.remainingLabel }}</span>
              </div>
              <div
                class="mt-1.5 h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"
                role="progressbar"
                :aria-valuenow="w.width"
                aria-valuemin="0"
                aria-valuemax="100"
                :aria-label="w.label"
              >
                <div
                  class="h-full rounded-full transition-all duration-500"
                  :class="w.barClass"
                  :style="{ width: w.width + '%' }"
                ></div>
              </div>
              <p v-if="w.resetsLabel" class="mt-1 text-[11px] text-gray-400 dark:text-gray-500">
                {{ w.resetsLabel }}
              </p>
            </div>
          </template>
          <div v-else class="rounded-lg bg-gray-50 p-4 text-center dark:bg-dark-800">
            <p class="text-xs font-medium text-gray-600 dark:text-gray-300">
              {{ t('accountQuota.noUsage') }}
            </p>
            <p class="mt-1 text-[11px] text-gray-400 dark:text-gray-500">
              {{ t('accountQuota.noUsageHint') }}
            </p>
          </div>
        </div>
        <p
          v-if="row.account.usage_updated_at"
          class="mt-4 border-t border-gray-100 pt-3 text-[11px] text-gray-400 dark:border-dark-700 dark:text-gray-500"
        >
          {{ t('accountQuota.lastUpdated', { time: formatTime(row.account.usage_updated_at) }) }}
        </p>
      </div>
    </div>
  </AppLayout>
</template>



<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import userAccountQuotaAPI, {
  type AccountQuotaWindow,
  type UserAccountQuota,
} from '@/api/accountQuota'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

/** 单行渲染用的额度窗口视图。 */
interface WindowRow {
  key: string
  label: string
  /** 剩余百分比（0-100），驱动进度条宽度。 */
  width: number
  remainingLabel: string
  toneClass: string
  barClass: string
  resetsLabel: string | null
}

interface AccountRow {
  account: UserAccountQuota
  windows: WindowRow[]
}

const { t, locale } = useI18n()
const appStore = useAppStore()

const accounts = ref<UserAccountQuota[]>([])
const loading = ref(false)
const lastUpdated = ref<Date | null>(null)
let abortController: AbortController | null = null

/** 展示顺序与后端 JSON 字段一一对应。 */
const WINDOW_DEFS: Array<{ key: keyof NonNullable<UserAccountQuota['usage']>; labelKey: string }> = [
  { key: 'five_hour', labelKey: 'accountQuota.windows.fiveHour' },
  { key: 'seven_day', labelKey: 'accountQuota.windows.sevenDay' },
  { key: 'seven_day_sonnet', labelKey: 'accountQuota.windows.sevenDaySonnet' },
  { key: 'seven_day_fable', labelKey: 'accountQuota.windows.sevenDayFable' },
  { key: 'thirty_day', labelKey: 'accountQuota.windows.thirtyDay' },
  { key: 'gemini_shared_daily', labelKey: 'accountQuota.windows.geminiSharedDaily' },
  { key: 'gemini_pro_daily', labelKey: 'accountQuota.windows.geminiProDaily' },
  { key: 'gemini_flash_daily', labelKey: 'accountQuota.windows.geminiFlashDaily' },
]

const PLATFORM_LABELS: Record<string, string> = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
  gemini: 'Gemini',
  grok: 'Grok',
  antigravity: 'Antigravity',
  composite: 'Composite',
  ollama: 'Ollama',
  opencode: 'OpenCode',
}

function platformLabel(platform: string): string {
  return PLATFORM_LABELS[platform] || platform
}

function groupNames(account: UserAccountQuota): string {
  return account.groups.map((g) => g.name).join(' / ')
}

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

function formatPercent(value: number): string {
  const rounded = Math.round(value * 10) / 10
  return `${Number.isInteger(rounded) ? rounded : rounded.toFixed(1)}%`
}

function formatTime(input: string | Date | null | undefined): string {
  if (input === null || input === undefined || input === '') return ''
  const date = typeof input === 'string' ? new Date(input) : input
  if (Number.isNaN(date.getTime())) return ''
  const lang = locale.value || 'en'
  const bcp47 = lang.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
  return date.toLocaleString(bcp47, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function buildWindows(account: UserAccountQuota): WindowRow[] {
  const usage = account.usage
  if (!usage) return []
  const out: WindowRow[] = []
  for (const def of WINDOW_DEFS) {
    const window = usage[def.key] as AccountQuotaWindow | null | undefined
    if (!window || typeof window.utilization !== 'number') continue
    const used = clampPercent(window.utilization)
    const remaining = clampPercent(100 - used)
    let toneClass: string
    let barClass: string
    if (remaining > 50) {
      toneClass = 'text-emerald-600 dark:text-emerald-400'
      barClass = 'bg-emerald-500'
    } else if (remaining > 20) {
      toneClass = 'text-amber-600 dark:text-amber-400'
      barClass = 'bg-amber-500'
    } else {
      toneClass = 'text-red-600 dark:text-red-400'
      barClass = 'bg-red-500'
    }
    out.push({
      key: def.key,
      label: t(def.labelKey),
      width: remaining,
      remainingLabel:
        remaining <= 0
          ? t('accountQuota.exhausted')
          : t('accountQuota.remaining', { percent: formatPercent(remaining) }),
      toneClass,
      barClass,
      resetsLabel: window.resets_at
        ? t('accountQuota.resetsAt', { time: formatTime(window.resets_at) })
        : null,
    })
  }
  return out
}

const rows = computed<AccountRow[]>(() =>
  accounts.value.map((account) => ({ account, windows: buildWindows(account) })),
)

async function load(silent = false) {
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  if (!silent) loading.value = true
  try {
    const list = await userAccountQuotaAPI.list({ signal: ctrl.signal })
    if (ctrl.signal.aborted || abortController !== ctrl) return
    accounts.value = list
    lastUpdated.value = new Date()
  } catch (err: unknown) {
    const e = err as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(err, t('accountQuota.loadError')))
  } finally {
    if (abortController === ctrl) {
      loading.value = false
      abortController = null
    }
  }
}

onMounted(() => load())
onBeforeUnmount(() => abortController?.abort())
</script>
