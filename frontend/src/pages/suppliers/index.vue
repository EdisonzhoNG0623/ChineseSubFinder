<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">SUPPLIER OPERATIONS</div>
        <h1>字幕源运行状态</h1>
        <p>连接性、命中情况和当日配额集中展示；检测在后台运行，不会阻塞页面。</p>
      </div>
      <div class="page-heading__actions row items-center q-gutter-sm">
        <div class="refresh-status text-caption" :class="isStale ? 'text-warning' : 'text-grey-7'" role="status">
          <q-icon :name="isStale ? 'schedule' : 'sync'" /> {{ refreshStatus }}
        </div>
        <q-btn flat icon="settings" label="配置字幕源" to="/settings?tab=subSource" />
        <q-btn flat round icon="refresh" :loading="loading" aria-label="刷新字幕源状态" @click="load(false)">
          <q-tooltip>刷新字幕源状态</q-tooltip>
        </q-btn>
        <q-btn
          unelevated
          color="primary"
          icon="health_and_safety"
          label="立即检测"
          :loading="checking"
          @click="check"
        />
      </div>
    </section>

    <div v-if="loadError" class="status-strip status-strip--danger q-mb-lg" role="alert">
      <q-icon name="cloud_off" />
      <div class="col">
        <strong>字幕源状态加载失败</strong>
        <div class="text-caption">{{ loadError }}</div>
      </div>
      <q-btn flat dense label="重试" @click="load(false)" />
    </div>

    <div class="metric-grid q-mb-lg">
      <div v-for="item in headline" :key="item.label">
        <q-card flat bordered class="metric-card">
          <q-card-section>
            <div class="metric-label">{{ item.label }}</div>
            <div class="metric-value" :class="item.className">{{ item.value }}</div>
          </q-card-section>
        </q-card>
      </div>
    </div>

    <q-card flat bordered class="surface-card">
      <q-table
        :rows="sortedRows"
        :columns="columns"
        row-key="name"
        flat
        :loading="loading"
        :pagination="{ rowsPerPage: 0 }"
        hide-bottom
      >
        <template #no-data>
          <div class="full-width empty-state">
            <div>
              <q-icon name="hub" size="38px" class="empty-state__icon" />
              <div class="empty-state__title">暂无字幕源状态</div>
              <div>点击“立即检测”获取连通性与延迟。</div>
            </div>
          </div>
        </template>
        <template #body-cell-status="{ row }">
          <q-td>
            <q-badge :color="healthMeta(row.health).color" outline>{{ healthMeta(row.health).label }}</q-badge>
            <q-badge v-if="row.attention_required" color="warning" text-color="dark" class="q-ml-xs">需关注</q-badge>
            <div v-if="row.status_message" class="text-caption text-grey-7 q-mt-xs">{{ row.status_message }}</div>
            <div v-if="row.not_attempted_reason" class="text-caption text-warning q-mt-xs">
              {{ row.not_attempted_reason }}
            </div>
            <div v-if="row.last_error_code" class="text-caption text-negative q-mt-xs">
              最近错误：{{ row.last_error_summary || errorCodeLabel(row.last_error_code) }}
              <span v-if="isRealTime(row.last_error_at)"> · {{ formatTime(row.last_error_at) }}</span>
            </div>
          </q-td>
        </template>
        <template #body-cell-activity="{ row }">
          <q-td>
            <div>最近搜索：{{ formatTime(row.last_attempt_at) }}</div>
            <div class="text-caption text-grey-7">最近检测：{{ formatTime(row.last_checked_at) }}</div>
            <div v-if="cooldownRemaining(row)" class="text-caption text-warning">
              冷却剩余 {{ cooldownRemaining(row) }} · 至 {{ formatTime(activeCooldownUntil(row)) }}
            </div>
          </q-td>
        </template>
        <template #body-cell-usage="{ row }">
          <q-td>
            <div>{{ usageText(row) }}</div>
            <q-linear-progress
              v-if="row.daily_limit > 0"
              rounded
              size="6px"
              class="q-mt-xs"
              :value="usageRatio(row)"
              :color="usageRatio(row) >= 0.8 ? 'warning' : 'primary'"
            />
          </q-td>
        </template>
        <template #body-cell-hit="{ row }">
          <q-td>
            <div>{{ row.candidate_hits }} 次命中 · {{ row.candidates }} 个候选</div>
            <div v-if="row.attempt_state === 'NOT_ATTEMPTED'" class="text-caption text-grey-7">尚无实际搜索请求</div>
            <div class="text-caption text-positive">
              选中 {{ row.selections || 0 }} 次 · 保存 {{ row.saves || 0 }} 次 · 转化 {{ conversionText(row) }}
            </div>
            <div class="text-caption text-grey-7">
              {{ row.empty_results }} 次空结果 / {{ row.errors }} 次错误 / {{ row.timeouts }} 次超时
            </div>
            <div v-if="row.cache_hits || row.early_stops" class="text-caption text-primary">
              缓存命中 {{ row.cache_hits || 0 }} 次 · 强匹配提前结束 {{ row.early_stops || 0 }} 次
            </div>
            <div v-if="row.circuit_skips" class="text-caption text-warning">熔断已跳过 {{ row.circuit_skips }} 次</div>
          </q-td>
        </template>
        <template #body-cell-latency="{ row }">
          <q-td>
            <div>{{ row.average_attempt_millis > 0 ? `平均 ${formatDuration(row.average_attempt_millis)}` : '—' }}</div>
            <div class="text-caption text-grey-7">
              P95 {{ formatDuration(row.p95_attempt_millis) }} · 当前预算 {{ formatDuration(row.search_budget_millis) }}
            </div>
            <div class="text-caption text-grey-7">连通性检测 {{ formatDuration(row.latency_millis) }}</div>
          </q-td>
        </template>
        <template #body-cell-capabilities="{ row }">
          <q-td
            ><q-chip v-for="capability in row.capabilities" :key="capability" dense size="sm">{{
              capability
            }}</q-chip></q-td
          >
        </template>
      </q-table>
    </q-card>
    <div class="text-caption text-grey-7 q-mt-sm" role="note">
      数据每 15
      秒刷新，页面隐藏时暂停。命中、选中、保存、缓存、耗时和熔断聚合统计会持久化；错误仅保留固定分类码，不记录媒体路径、候选名称、错误正文或密钥。
    </div>
  </q-page>
</template>

<script setup>
import { computed, onBeforeUnmount, ref } from 'vue';
import dayjs from 'dayjs';
import SupplierApi from 'src/api/SupplierApi';
import useInterval from 'src/composables/use-interval';
import { SystemMessage } from 'src/utils/message';

const rows = ref([]);
const loading = ref(false);
const checking = ref(false);
const loadError = ref('');
const generatedAt = ref('');
const clock = ref(Date.now());
let requestSequence = 0;
let checkRefreshTimer = null;
let isUnmounted = false;
const columns = [
  { name: 'name', label: '字幕源', field: 'display_name', align: 'left' },
  { name: 'status', label: '状态', field: 'health', align: 'left' },
  { name: 'activity', label: '最近活动', align: 'left' },
  { name: 'usage', label: '今日配额', align: 'left' },
  { name: 'hit', label: '运行命中', align: 'left' },
  { name: 'latency', label: '运行耗时', align: 'left' },
  { name: 'capabilities', label: '能力', align: 'left' },
];

const healthMeta = (health) =>
  ({
    HEALTHY: { label: '可用', color: 'positive' },
    DEGRADED: { label: '降级', color: 'warning' },
    UNHEALTHY: { label: '不可用', color: 'negative' },
    COOLDOWN: { label: '冷却中', color: 'orange' },
    DISABLED: { label: '未启用', color: 'grey' },
    RETIRED: { label: '公共域名失效', color: 'negative' },
    UNAVAILABLE_IN_MODE: { label: '当前模式不可用', color: 'grey' },
    UNKNOWN: { label: '待检测', color: 'blue-grey' },
  }[health] || { label: health || '未知', color: 'grey' });

const requiresAttention = (row) =>
  row.attention_required || ['DEGRADED', 'UNHEALTHY', 'RETIRED', 'COOLDOWN'].includes(row.health);
const healthRank = (row) => {
  const severity =
    { UNHEALTHY: 0, RETIRED: 1, DEGRADED: 2, COOLDOWN: 3, UNKNOWN: 4, HEALTHY: 5, UNAVAILABLE_IN_MODE: 6, DISABLED: 7 }[
      row.health
    ] ?? 4;
  return (requiresAttention(row) ? 0 : 10) + severity;
};
const sortedRows = computed(() =>
  [...rows.value].sort(
    (left, right) => healthRank(left) - healthRank(right) || left.display_name.localeCompare(right.display_name)
  )
);

const headline = computed(() => [
  { label: '已配置', value: rows.value.filter((item) => item.configured).length },
  {
    label: '健康可用',
    value: rows.value.filter((item) => item.health === 'HEALTHY').length,
    className: 'text-positive',
  },
  {
    label: '需关注',
    value: rows.value.filter(requiresAttention).length,
    className: 'text-warning',
  },
  {
    label: '累计保存',
    value: rows.value.reduce((sum, item) => sum + (item.saves || 0), 0),
    className: 'text-positive',
  },
]);
const usageRatio = (row) => Math.min(1, row.daily_limit > 0 ? row.daily_used / row.daily_limit : 0);
const usageText = (row) =>
  row.daily_limit < 0 ? `${row.daily_used} / 不限` : `${row.daily_used} / ${row.daily_limit}`;
const formatDuration = (millis) => {
  if (!millis) return '—';
  if (millis < 1000) return `${millis} ms`;
  return `${(millis / 1000).toFixed(millis < 10000 ? 1 : 0)} s`;
};
const conversionText = (row) => {
  if (!row.selections) return '—';
  return `${Math.round(((row.saves || 0) / row.selections) * 100)}%`;
};
const isRealTime = (value) => value && dayjs(value).isValid() && dayjs(value).year() > 1;
const formatTime = (value) => (isRealTime(value) ? dayjs(value).format('MM-DD HH:mm:ss') : '尚无记录');
const activeCooldownUntil = (row) => {
  const candidates = [row.cooldown_until, row.circuit_open_until]
    .filter(isRealTime)
    .map((value) => dayjs(value).valueOf())
    .filter((value) => value > clock.value);
  return candidates.length ? new Date(Math.max(...candidates)).toISOString() : '';
};
const cooldownRemaining = (row) => {
  const until = activeCooldownUntil(row);
  if (!until) return '';
  const seconds = Math.max(0, Math.ceil((dayjs(until).valueOf() - clock.value) / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟`;
  return `${Math.floor(seconds / 3600)} 小时 ${Math.ceil((seconds % 3600) / 60)} 分钟`;
};
const errorCodeLabel = (code) =>
  ({
    TIMEOUT: '请求超时',
    QUOTA: '配额或频率限制',
    AUTH: '认证失败',
    BLOCKED: '验证码或访问限制',
    NETWORK: '网络连接失败',
    PROVIDER: '字幕源响应异常',
    UNKNOWN: '未分类错误',
  }[code] || '未分类错误');
const isStale = computed(() => !generatedAt.value || clock.value - dayjs(generatedAt.value).valueOf() > 45000);
const refreshStatus = computed(() => {
  if (!generatedAt.value) return '等待首次更新';
  const label = dayjs(generatedAt.value).format('HH:mm:ss');
  return isStale.value ? `数据可能已陈旧 · ${label}` : `更新于 ${label}`;
});

const load = async (silent = false) => {
  requestSequence += 1;
  const sequence = requestSequence;
  clock.value = Date.now();
  if (!silent) loading.value = true;
  const [res, err] = await SupplierApi.getDiagnostics();
  if (sequence !== requestSequence || isUnmounted) return;
  loading.value = false;
  if (err) {
    loadError.value = err.message || '无法读取字幕源状态';
    return;
  }
  loadError.value = '';
  rows.value = res.data || [];
  checking.value = !!res.is_checking;
  generatedAt.value = res.generated_at || new Date().toISOString();
};
const check = async () => {
  // Invalidate an older diagnostics response so it cannot clear the newly
  // entered checking state while the start request is in flight.
  requestSequence += 1;
  loading.value = false;
  checking.value = true;
  const [, err] = await SupplierApi.check();
  if (isUnmounted) return;
  const status = err?.error?.status ?? err?.response?.status ?? err?.status;
  if (err && status !== 409) {
    checking.value = false;
    SystemMessage.error(err.message);
    return;
  }
  SystemMessage.success('字幕源检测已启动');
  if (checkRefreshTimer !== null) clearTimeout(checkRefreshTimer);
  checkRefreshTimer = setTimeout(() => {
    checkRefreshTimer = null;
    load(true);
  }, 1200);
};

onBeforeUnmount(() => {
  isUnmounted = true;
  requestSequence += 1;
  if (checkRefreshTimer !== null) clearTimeout(checkRefreshTimer);
  checkRefreshTimer = null;
});

useInterval(() => load(rows.value.length > 0), 15000);
useInterval(() => {
  clock.value = Date.now();
}, 1000);
</script>

<style scoped>
.refresh-status {
  white-space: nowrap;
}
@media (max-width: 760px) {
  .refresh-status {
    width: 100%;
  }
}
</style>
