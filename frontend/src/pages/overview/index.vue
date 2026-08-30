<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">OPERATIONS OVERVIEW</div>
        <h1>字幕自动化总览</h1>
        <p>队列、定时扫描和字幕源状态每 15 秒自动更新。</p>
      </div>
      <q-btn-toggle
        class="page-heading__actions"
        v-model="days"
        unelevated
        toggle-color="primary"
        :options="dayOptions"
        @update:model-value="load"
      />
    </section>

    <div v-if="loadError" class="status-strip status-strip--danger q-mb-lg" role="alert">
      <q-icon name="cloud_off" />
      <div class="col">
        <strong>总览数据暂时不可用</strong>
        <div class="text-caption">{{ loadError }}</div>
      </div>
      <q-btn flat dense label="重试" :loading="loading" @click="load(false)" />
    </div>

    <q-linear-progress v-if="loading && !hasLoaded" indeterminate color="primary" class="q-mb-lg" />

    <q-card flat bordered class="daemon-card q-mb-lg">
      <q-card-section class="row items-start q-col-gutter-md">
        <div class="col-auto q-pt-sm">
          <div
            class="status-orb"
            :class="isRunning ? 'is-running' : ''"
            role="status"
            :aria-label="runtimeHeadline"
          ></div>
        </div>
        <div class="col-12 col-sm">
          <div class="text-h6">{{ runtimeHeadline }}</div>
          <div class="text-grey-7">{{ runtimeDetail }}</div>
          <div v-if="overview.schedule?.current_jobs?.length" class="current-jobs q-mt-sm" aria-label="当前任务">
            <q-chip
              v-for="job in overview.schedule.current_jobs"
              :key="job.id"
              dense
              outline
              color="primary"
              icon="downloading"
            >
              {{ job.name }}
            </q-chip>
          </div>
        </div>
        <div class="col-auto daemon-actions">
          <q-btn v-if="isRunning" outline color="negative" label="停止" :loading="submitting" @click="stopJobs" />
          <q-btn
            v-else
            unelevated
            color="primary"
            :label="isStopping ? '正在停止' : '立即运行'"
            :disable="!canStart"
            :loading="submitting"
            @click="startJobs"
          />
        </div>
      </q-card-section>
      <q-separator />
      <q-card-section class="schedule-grid">
        <div class="schedule-item">
          <div class="metric-label">下一轮媒体扫描</div>
          <div class="schedule-value">{{ formatScheduleTime(overview.schedule?.next_scan_at) }}</div>
        </div>
        <div class="schedule-item">
          <div class="metric-label">最早字幕重试</div>
          <div class="schedule-value">{{ retryScheduleText }}</div>
        </div>
        <div class="schedule-item">
          <div class="metric-label">最近成功下载</div>
          <div class="schedule-value">{{ formatScheduleTime(overview.schedule?.last_success_at) }}</div>
        </div>
        <div class="schedule-item">
          <div class="metric-label">系统下一动作</div>
          <div class="schedule-value">{{ nextActionText }}</div>
        </div>
        <div v-if="overview.schedule?.last_cycle_at" class="schedule-item">
          <div class="metric-label">最近调度轮次</div>
          <div class="schedule-value">{{ formatScheduleTime(overview.schedule.last_cycle_at) }}</div>
        </div>
      </q-card-section>
    </q-card>

    <div class="metric-grid q-mb-lg">
      <router-link v-for="metric in queueMetrics" :key="metric.label" :to="metric.to" class="metric-link">
        <q-card flat bordered class="metric-card"
          ><q-card-section>
            <div class="metric-label">{{ metric.label }}</div>
            <div class="metric-value" :class="metric.className">{{ metric.value }}</div>
            <div class="metric-note">{{ metric.note }}</div>
          </q-card-section></q-card
        >
      </router-link>
    </div>

    <div class="row q-col-gutter-lg">
      <div class="col-12 col-lg-7">
        <q-card flat bordered class="surface-card full-height">
          <q-card-section class="row items-center"
            ><div>
              <div class="section-title">下载结果趋势</div>
              <div class="text-caption text-grey-7">按任务最终结果聚合</div>
            </div>
            <q-space /><q-btn flat dense color="primary" label="查看队列" to="/jobs"
          /></q-card-section>
          <q-separator />
          <q-card-section v-if="outcomeDays.length" class="trend-list">
            <div v-for="item in outcomeDays" :key="item.day" class="trend-row">
              <div class="trend-day">{{ item.day.slice(5) }}</div>
              <div class="trend-track">
                <div v-if="item.success" class="trend-success" :style="{ width: `${barWidth(item.success)}%` }"></div>
                <div v-if="item.failure" class="trend-failure" :style="{ width: `${barWidth(item.failure)}%` }"></div>
              </div>
              <div class="trend-count">{{ item.success }} 成功 / {{ item.failure }} 未命中或失败</div>
            </div>
          </q-card-section>
          <q-card-section v-else class="empty-state">尚无结果数据，任务完成后会在这里形成趋势。</q-card-section>
        </q-card>
      </div>

      <div class="col-12 col-lg-5 column q-gutter-y-lg">
        <q-card flat bordered class="surface-card">
          <q-card-section class="row items-center"
            ><div class="section-title">字幕源健康</div>
            <q-space /><q-btn flat dense color="primary" label="管理" to="/suppliers"
          /></q-card-section>
          <q-separator />
          <q-list separator>
            <q-item v-for="supplier in supplierHighlights" :key="supplier.name">
              <q-item-section
                ><q-item-label>{{ supplier.display_name }}</q-item-label
                ><q-item-label caption>{{
                  supplier.status_message || supplier.capabilities.join(' · ')
                }}</q-item-label></q-item-section
              >
              <q-item-section side
                ><q-badge :color="supplierColor(supplier.health)" outline>{{
                  supplierLabel(supplier.health)
                }}</q-badge></q-item-section
              >
            </q-item>
          </q-list>
        </q-card>

        <q-card flat bordered class="surface-card">
          <q-card-section class="row items-center"
            ><div>
              <div class="section-title">AI 歧义识别</div>
              <div class="text-caption text-grey-7">只在确定性规则无法区分候选时调用</div>
            </div>
            <q-space /><q-badge :color="overview.ai?.enabled ? 'positive' : 'grey'">{{
              overview.ai?.enabled ? '已启用' : '未启用'
            }}</q-badge></q-card-section
          >
          <q-separator />
          <q-card-section class="row justify-between text-center">
            <div>
              <div class="text-h6">{{ overview.ai?.attempts || 0 }}</div>
              <div class="text-caption">调用</div>
            </div>
            <div>
              <div class="text-h6 text-positive">{{ overview.ai?.matches || 0 }}</div>
              <div class="text-caption">匹配</div>
            </div>
            <div>
              <div class="text-h6">{{ overview.ai?.abstentions || 0 }}</div>
              <div class="text-caption">弃权</div>
            </div>
            <div>
              <div class="text-h6 text-negative">{{ overview.ai?.errors || 0 }}</div>
              <div class="text-caption">错误</div>
            </div>
          </q-card-section>
        </q-card>
      </div>
    </div>
  </q-page>
</template>

<script setup>
import { computed, reactive, ref } from 'vue';
import dayjs from 'dayjs';
import { useQuasar } from 'quasar';
import JobApi from 'src/api/JobApi';
import OverviewApi from 'src/api/OverviewApi';
import useInterval from 'src/composables/use-interval';
import { SystemMessage } from 'src/utils/message';

const $q = useQuasar();
const overview = reactive({ queue: {}, schedule: {}, suppliers: [], outcomes: [], ai: {} });
const days = ref(7);
const submitting = ref(false);
const loading = ref(false);
const hasLoaded = ref(false);
const loadError = ref('');
const dayOptions = [
  { label: '24 小时', value: 1 },
  { label: '7 天', value: 7 },
  { label: '30 天', value: 30 },
];
let requestSequence = 0;
const isRunning = computed(() => overview.schedule?.status === 'running');
const isStopping = computed(() => overview.schedule?.status === 'stopping');
const canStart = computed(
  () => hasLoaded.value && !loadError.value && overview.schedule?.status === 'stopped' && !submitting.value
);
const runtimeHeadline = computed(() => {
  const phase = overview.schedule?.phase;
  const downloading = overview.queue?.downloading || 0;
  const ready = overview.queue?.ready_now || 0;
  switch (phase) {
    case 'STOPPED':
      return '自动下载已暂停';
    case 'STOPPING':
      return '正在安全停止任务';
    case 'DOWNLOADING':
      return `正在下载 · ${downloading} 个任务进行中`;
    case 'PROCESSING':
      return '正在检查并领取下载任务';
    case 'READY':
      return ready ? `正常运行 · ${ready} 个任务可立即处理` : '正常运行 · 队列刷新任务可立即处理';
    case 'BACKOFF':
      return '正常空闲 · 0 个下载中 · 等待退避任务';
    case 'SCHEDULED':
      return '正常空闲 · 已安排下一次队列刷新';
    default:
      return isRunning.value ? '正常空闲 · 当前没有待下载任务' : '正在读取运行状态';
  }
});
const runtimeDetail = computed(() => {
  const workers = overview.schedule?.active_workers || 0;
  const lastSuccess = overview.schedule?.last_success_at;
  const workerText = workers ? `${workers} 个工作线程活跃` : '当前无活跃工作线程';
  return lastSuccess
    ? `${workerText} · 最近成功 ${dayjs(lastSuccess).format('MM-DD HH:mm')}`
    : `${workerText} · 暂无成功记录`;
});
const queueMetrics = computed(() => [
  { label: '队列总数', value: overview.queue?.total || 0, note: '所有状态任务', to: '/jobs' },
  {
    label: '正在下载',
    value: overview.queue?.downloading || 0,
    note: '当前落盘任务',
    className: 'text-primary',
    to: { path: '/jobs', query: { queueState: 'downloading' } },
  },
  {
    label: '可立即处理',
    value: overview.queue?.ready_now || 0,
    note: overview.queue?.oldest_ready_at
      ? `最早等待于 ${dayjs(overview.queue.oldest_ready_at).format('MM-DD HH:mm')}`
      : '未受退避限制',
    className: 'text-primary',
    to: { path: '/jobs', query: { queueState: 'ready' } },
  },
  {
    label: '等待重试',
    value: overview.queue?.backoff_waiting || 0,
    note: overview.queue?.earliest_retry_at
      ? `最早 ${dayjs(overview.queue.earliest_retry_at).format('MM-DD HH:mm')}`
      : '已安排下次尝试',
    className: 'text-warning',
    to: { path: '/jobs', query: { queueState: 'backoff_waiting' } },
  },
  {
    label: '未命中字幕',
    value: overview.queue?.actionable_by_error_category?.NO_SUBTITLE || 0,
    note: '仅统计仍需处置任务',
    className: 'text-negative',
    to: { path: '/jobs', query: { errorCategory: 'NO_SUBTITLE', actionableOnly: 'true' } },
  },
  {
    label: '集号回退就绪',
    value: overview.queue?.numbering_ready || 0,
    note: `等待剧集 ${overview.queue?.episode_waiting || 0} 项`,
    className: 'text-primary',
    to: { path: '/jobs', query: { queueState: 'numbering_ready' } },
  },
  {
    label: '元数据阻塞',
    value: overview.queue?.metadata_blocked || 0,
    note: '未调用字幕源，需检查 NFO',
    className: overview.queue?.metadata_blocked ? 'text-warning' : 'text-positive',
    to: { path: '/jobs', query: { queueState: 'metadata_blocked' } },
  },
  {
    label: '累计保存字幕',
    value: (overview.suppliers || []).reduce((sum, item) => sum + (item.saves || 0), 0),
    note: '字幕源聚合指标的历史累计值',
    className: 'text-positive',
    to: '/suppliers',
  },
]);
const isRealTime = (value) => value && dayjs(value).isValid() && dayjs(value).year() > 1;
const formatScheduleTime = (value) => (isRealTime(value) ? dayjs(value).format('MM-DD HH:mm') : '暂无权威记录');
const retryScheduleText = computed(() => {
  const value = overview.schedule?.next_retry_at;
  if (!isRealTime(value)) return '当前没有已安排重试';
  if (!dayjs(value).isAfter(dayjs())) return `已到执行时间 · ${dayjs(value).format('MM-DD HH:mm')}`;
  return dayjs(value).format('MM-DD HH:mm');
});
const nextActionText = computed(() => {
  if (['DOWNLOADING', 'PROCESSING', 'READY'].includes(overview.schedule?.phase)) return '现在';
  const value = overview.schedule?.next_action_at;
  return isRealTime(value) ? dayjs(value).format('MM-DD HH:mm') : '等待事件触发';
});
const outcomeDays = computed(() => {
  const map = {};
  (overview.outcomes || []).forEach((item) => {
    map[item.which_day] ||= { day: item.which_day, success: 0, failure: 0 };
    if (item.outcome === 'SUCCESS') map[item.which_day].success += item.count;
    else map[item.which_day].failure += item.count;
  });
  return Object.values(map).sort((a, b) => a.day.localeCompare(b.day));
});
const maxOutcome = computed(() => Math.max(1, ...outcomeDays.value.map((item) => item.success + item.failure)));
const barWidth = (value) => (value / maxOutcome.value) * 100;
const healthRank = (health) =>
  ({ UNHEALTHY: 0, RETIRED: 1, DEGRADED: 2, COOLDOWN: 3, HEALTHY: 4, UNKNOWN: 5, UNAVAILABLE_IN_MODE: 6, DISABLED: 7 }[
    health
  ] ?? 5);
const supplierHighlights = computed(() =>
  [...(overview.suppliers || [])]
    .sort(
      (a, b) =>
        Number(!!b.attention_required) - Number(!!a.attention_required) || healthRank(a.health) - healthRank(b.health)
    )
    .slice(0, 5)
);
const supplierColor = (health) =>
  ({
    HEALTHY: 'positive',
    DEGRADED: 'warning',
    UNHEALTHY: 'negative',
    RETIRED: 'negative',
    COOLDOWN: 'orange',
    UNAVAILABLE_IN_MODE: 'grey',
  }[health] || 'grey');
const supplierLabel = (health) =>
  ({
    HEALTHY: '可用',
    DEGRADED: '降级',
    UNHEALTHY: '不可用',
    RETIRED: '域名失效',
    COOLDOWN: '冷却中',
    UNAVAILABLE_IN_MODE: '当前模式不可用',
    DISABLED: '未启用',
    UNKNOWN: '待检测',
  }[health] || health);
const load = async (silent = true) => {
  requestSequence += 1;
  const sequence = requestSequence;
  const requestedDays = days.value;
  if (!silent) loading.value = true;
  const [res, err] = await OverviewApi.get(requestedDays);
  if (sequence !== requestSequence) return;
  loading.value = false;
  if (err) {
    loadError.value = err.message || '无法连接到服务端';
    return;
  }
  loadError.value = '';
  hasLoaded.value = true;
  Object.assign(overview, res);
};
const changeDaemon = (action, message) =>
  $q.dialog({ title: message, cancel: true }).onOk(async () => {
    submitting.value = true;
    const [, err] = await action();
    submitting.value = false;
    if (err) {
      SystemMessage.error(err.message);
      return;
    }
    SystemMessage.success('操作成功');
    load();
  });
const startJobs = () => {
  if (!canStart.value) return;
  changeDaemon(() => JobApi.start(), '是否立即运行？');
};
const stopJobs = () => changeDaemon(() => JobApi.stop(), '是否停止守护进程？');

useInterval(() => load(hasLoaded.value), 15000);
</script>

<style scoped>
.schedule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 20px;
}
.schedule-item {
  min-width: 0;
}
.schedule-value {
  margin-top: 4px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
.current-jobs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.current-jobs .q-chip {
  max-width: min(100%, 360px);
}
.metric-link {
  min-width: 0;
  color: inherit;
  text-decoration: none;
}
.metric-link .metric-card {
  transition: border-color 120ms ease, background-color 120ms ease;
}
.metric-link:hover .metric-card,
.metric-link:focus-visible .metric-card {
  border-color: var(--csf-primary) !important;
  background: var(--csf-primary-soft);
}
.metric-link:focus-visible {
  border-radius: var(--csf-radius-lg);
  outline: 3px solid rgba(43, 126, 93, 0.24);
  outline-offset: 3px;
}
@media (max-width: 760px) {
  .schedule-grid {
    grid-template-columns: 1fr;
    gap: 12px;
  }
  .daemon-actions {
    width: 100%;
  }
  .daemon-actions .q-btn {
    width: 100%;
  }
}
</style>
