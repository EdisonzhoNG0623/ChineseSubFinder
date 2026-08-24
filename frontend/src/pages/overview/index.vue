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
      <q-card-section class="row items-center q-col-gutter-md">
        <div class="col-auto"><div class="status-orb" :class="isRunning ? 'is-running' : ''"></div></div>
        <div class="col">
          <div class="text-h6">守护进程{{ isRunning ? '正在运行' : '已停止' }}</div>
          <div class="text-grey-7">{{ scheduleHint }}</div>
        </div>
        <div class="text-right q-mr-lg">
          <div class="text-caption text-grey-7">活跃下载任务</div>
          <div class="text-h5">{{ overview.schedule?.active_workers || 0 }}</div>
        </div>
        <q-btn v-if="isRunning" outline color="negative" label="停止" :loading="submitting" @click="stopJobs" />
        <q-btn v-else unelevated color="primary" label="立即运行" :loading="submitting" @click="startJobs" />
      </q-card-section>
    </q-card>

    <div class="metric-grid q-mb-lg">
      <div v-for="metric in queueMetrics" :key="metric.label">
        <q-card flat bordered class="metric-card"
          ><q-card-section>
            <div class="metric-label">{{ metric.label }}</div>
            <div class="metric-value" :class="metric.className">{{ metric.value }}</div>
            <div class="metric-note">{{ metric.note }}</div>
          </q-card-section></q-card
        >
      </div>
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
const isRunning = computed(() => overview.schedule?.status === 'running');
const scheduleHint = computed(() => {
  if (!isRunning.value) return '自动扫描和队列消费当前暂停';
  if (!overview.schedule?.next_scan_at) return '正在等待下一次扫描安排';
  return `下次扫描：${dayjs(overview.schedule.next_scan_at).format('MM-DD HH:mm')}`;
});
const queueMetrics = computed(() => [
  { label: '队列总数', value: overview.queue?.total || 0, note: '所有状态任务' },
  { label: '可立即处理', value: overview.queue?.ready_now || 0, note: '未受退避限制', className: 'text-primary' },
  { label: '等待重试', value: overview.queue?.retry_scheduled || 0, note: '已安排下次尝试', className: 'text-warning' },
  {
    label: '未命中字幕',
    value: overview.queue?.by_error_category?.NO_SUBTITLE || 0,
    note: '可通过源与识别优化',
    className: 'text-negative',
  },
]);
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
  ({ UNHEALTHY: 0, RETIRED: 1, DEGRADED: 2, COOLDOWN: 3, HEALTHY: 4, UNKNOWN: 5, DISABLED: 6 }[health] ?? 5);
const supplierHighlights = computed(() =>
  [...(overview.suppliers || [])].sort((a, b) => healthRank(a.health) - healthRank(b.health)).slice(0, 5)
);
const supplierColor = (health) =>
  ({ HEALTHY: 'positive', DEGRADED: 'warning', UNHEALTHY: 'negative', RETIRED: 'negative', COOLDOWN: 'orange' }[
    health
  ] || 'grey');
const supplierLabel = (health) =>
  ({
    HEALTHY: '可用',
    DEGRADED: '降级',
    UNHEALTHY: '不可用',
    RETIRED: '域名失效',
    COOLDOWN: '冷却中',
    DISABLED: '未启用',
    UNKNOWN: '待检测',
  }[health] || health);
const load = async (silent = true) => {
  if (!silent) loading.value = true;
  const [res, err] = await OverviewApi.get(days.value);
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
const startJobs = () => changeDaemon(() => JobApi.start(), '是否立即运行？');
const stopJobs = () => changeDaemon(() => JobApi.stop(), '是否停止守护进程？');

useInterval(() => load(hasLoaded.value), 15000);
</script>
