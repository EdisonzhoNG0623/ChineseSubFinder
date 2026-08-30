<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">DOWNLOAD QUEUE</div>
        <h1>下载队列</h1>
        <p>服务端筛选与分页，重试时间和失败原因可直接诊断。</p>
      </div>
      <div class="page-heading__actions row items-center q-gutter-sm">
        <div class="refresh-status text-caption" :class="isStale ? 'text-warning' : 'text-grey-7'" role="status">
          <q-icon :name="isStale ? 'schedule' : 'sync'" /> {{ refreshStatus }}
        </div>
        <q-btn flat round icon="refresh" :loading="loading" aria-label="刷新下载队列" @click="load()">
          <q-tooltip>刷新下载队列</q-tooltip>
        </q-btn>
      </div>
    </section>

    <div v-if="loadError" class="status-strip status-strip--danger q-mb-lg" role="alert">
      <q-icon name="cloud_off" />
      <div class="col">
        <strong>队列数据加载失败</strong>
        <div class="text-caption">{{ loadError }}</div>
      </div>
      <q-btn flat dense label="重试" @click="load" />
    </div>

    <div class="metric-grid q-mb-md">
      <q-card
        v-for="metric in summaryMetrics"
        :key="metric.label"
        flat
        bordered
        class="metric-card metric-action"
        :class="{ 'metric-card--active': metric.active }"
        role="button"
        tabindex="0"
        :aria-label="`${metric.label} ${metric.value}，筛选对应任务`"
        :aria-pressed="metric.active"
        @click="applyMetricFilter(metric)"
        @keydown.enter.prevent="applyMetricFilter(metric)"
        @keydown.space.prevent
        @keyup.space.prevent="applyMetricFilter(metric)"
      >
        <q-card-section>
          <div class="metric-label">{{ metric.label }}</div>
          <div class="metric-value" :class="metric.className">{{ metric.value }}</div>
          <div class="metric-note">{{ metric.note }}</div>
        </q-card-section>
      </q-card>
    </div>

    <q-card flat bordered class="surface-card">
      <q-card-section class="filter-bar row q-col-gutter-sm items-center">
        <div class="col-12 col-md">
          <q-input
            v-model="filters.search"
            debounce="450"
            dense
            outlined
            clearable
            placeholder="名称、路径或媒体 ID"
            prefix="搜索"
            ><template #prepend><q-icon name="search" /></template
          ></q-input>
        </div>
        <div class="col-6 col-sm-3 col-md-2">
          <q-select
            v-model="filters.status"
            :options="statusOptions"
            label="状态"
            dense
            outlined
            clearable
            emit-value
            map-options
          />
        </div>
        <div class="col-6 col-sm-3 col-md-2">
          <q-select
            v-model="filters.videoType"
            :options="videoTypeOptions"
            label="类型"
            dense
            outlined
            clearable
            emit-value
            map-options
          />
        </div>
        <div class="col-6 col-sm-3 col-md-2">
          <q-select
            v-model="filters.errorCategory"
            :options="errorOptions"
            label="失败原因"
            dense
            outlined
            clearable
            emit-value
            map-options
          />
        </div>
        <div class="col-6 col-sm-3 col-md-2">
          <q-select
            v-model="filters.queueState"
            :options="queueStateOptions"
            label="队列视图"
            dense
            outlined
            clearable
            emit-value
            map-options
          />
        </div>
        <div class="col-6 col-sm-3 col-md-2">
          <q-select
            v-model="filters.priority"
            :options="priorityOptions"
            label="优先级"
            dense
            outlined
            clearable
            emit-value
            map-options
          />
        </div>
      </q-card-section>

      <q-card-section v-if="filters.queueState" class="row items-center q-gutter-sm q-py-sm bg-blue-grey-1">
        <q-icon name="filter_alt" color="primary" />
        <span class="text-caption">指标筛选：{{ queueStateLabel(filters.queueState) }}</span>
        <q-btn flat dense size="sm" color="primary" label="清除" @click="filters.queueState = null" />
      </q-card-section>

      <q-separator />
      <q-card-section v-if="selected.length" class="row items-center q-gutter-sm bg-blue-grey-1">
        <span class="text-weight-medium">已选 {{ selected.length }} 项</span>
        <q-btn
          size="sm"
          outline
          color="primary"
          icon="expand_less"
          label="提高优先级"
          :disable="hasDownloadingSelection"
          @click="batchUpdatePriority('high')"
        />
        <q-btn
          size="sm"
          outline
          color="primary"
          icon="expand_more"
          label="降低优先级"
          :disable="hasDownloadingSelection"
          @click="batchUpdatePriority('low')"
        />
        <q-btn size="sm" outline color="primary" label="修改状态" @click="batchUpdateStatus" />
        <span v-if="hasDownloadingSelection" class="text-caption text-grey-7">下载中任务需先重置或忽略，不能直接改优先级</span>
      </q-card-section>

      <q-table
        v-model:pagination="pagination"
        v-model:selected="selected"
        :rows="rows"
        :columns="columns"
        :loading="loading"
        row-key="id"
        selection="multiple"
        binary-state-sort
        flat
        :rows-per-page-options="[20, 50, 100, 200]"
        @request="onRequest"
      >
        <template #body-cell-jobStatus="{ row }"
          ><q-td
            ><q-badge
              :text-color="row.job_status === JOB_STATUS_IGNORE ? 'dark' : 'white'"
              :style="{ backgroundColor: JOB_STATUS_COLOR_MAP[row.job_status] }"
              >{{ JOB_STATUS_MAP[row.job_status] }}</q-badge
            ></q-td
          ></template
        >
        <template #body-cell-name="{ row }"
          ><q-td class="job-name-cell"
            ><div class="text-weight-medium ellipsis">{{ row.video_name }}</div>
            <div v-if="row.identity?.is_anime" class="text-caption text-primary">动漫 · {{ identityText(row) }}</div>
            <div class="text-caption text-grey-7 ellipsis">{{ row.video_f_path }}</div></q-td
          ></template
        >
        <template #body-cell-priority="{ row }"
          ><q-td
            ><q-badge outline color="blue-grey">P{{ row.task_priority }}</q-badge></q-td
          ></template
        >
        <template #body-cell-nextAttemptAt="{ row }"
          ><q-td
            ><div>{{ retryText(row) }}</div>
            <q-badge v-if="row.retry?.category !== 'NONE'" outline :color="errorMeta(row.retry.category).color">{{
              errorMeta(row.retry.category).label
            }}</q-badge></q-td
          ></template
        >
        <template #body-cell-updatedAt="{ row }"
          ><q-td>{{ formatTime(row.update_time) }}</q-td></template
        >
        <template #body-cell-actions="{ row }"
          ><q-td class="sticky-action"><job-detail-btn-dialog :data="row" /><job-log-btn-dialog :data="row" /></q-td
        ></template>
        <template #no-data><div class="full-width empty-state">当前筛选条件下没有任务。</div></template>
      </q-table>
    </q-card>
  </q-page>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import dayjs from 'dayjs';
import { useQuasar } from 'quasar';
import { useRoute } from 'vue-router';
import JobApi from 'src/api/JobApi';
import JobLogBtnDialog from 'pages/jobs/JobLogBtnDialog';
import JobDetailBtnDialog from 'pages/jobs/JobDetailBtnDialog';
import useInterval from 'src/composables/use-interval';
import { SystemMessage } from 'src/utils/message';
import { VIDEO_TYPE_NAME_MAP } from 'src/constants/SettingConstants';
import {
  ERROR_CATEGORY_META,
  JOB_STATUS_COLOR_MAP,
  JOB_STATUS_DOWNLOADING,
  JOB_STATUS_IGNORE,
  JOB_STATUS_MAP,
  JOB_STATUS_OPTIONS,
  JOB_STATUS_PENDING,
} from 'src/constants/JobConstants';

const $q = useQuasar();
const route = useRoute();
const rows = ref([]);
const selected = ref([]);
const hasDownloadingSelection = computed(() =>
  selected.value.some((item) => item.job_status === JOB_STATUS_DOWNLOADING)
);
const loading = ref(false);
const loadError = ref('');
const generatedAt = ref('');
const clock = ref(Date.now());
const summary = reactive({
  total: 0,
  by_status: {},
  by_error_category: {},
  actionable_by_error_category: {},
  retry_scheduled: 0,
  backoff_waiting: 0,
  ready_now: 0,
});
const queryValue = (query, name) => (Array.isArray(query[name]) ? query[name][0] : query[name]);
const queryNumber = (query, name) => {
  const value = Number(queryValue(query, name));
  return Number.isInteger(value) ? value : null;
};
const hasOwn = (object, key) => key !== null && key !== undefined && Object.prototype.hasOwnProperty.call(object, key);
const queueStateValues = [
  'ready',
  'retry_scheduled',
  'backoff_waiting',
  'downloading',
  'numbering_ready',
  'metadata_blocked',
];
const filtersFromRoute = (query) => {
  const status = queryNumber(query, 'status');
  const videoType = queryNumber(query, 'videoType');
  const errorCategory = queryValue(query, 'errorCategory');
  const actionableOnly = queryValue(query, 'actionableOnly');
  const queueState = queryValue(query, 'queueState');
  const priority = queryValue(query, 'priority');
  return {
    search: queryValue(query, 'search') || '',
    status: hasOwn(JOB_STATUS_MAP, status) ? status : null,
    videoType: hasOwn(VIDEO_TYPE_NAME_MAP, videoType) ? videoType : null,
    errorCategory: hasOwn(ERROR_CATEGORY_META, errorCategory) ? errorCategory : null,
    actionableOnly: actionableOnly === 'true' ? true : null,
    queueState: queueStateValues.includes(queueState) ? queueState : null,
    priority: ['high', 'middle', 'low'].includes(priority) ? priority : null,
  };
};
const filters = reactive(filtersFromRoute(route.query));
watch(
  () => filters.errorCategory,
  (category) => {
    if (!category) filters.actionableOnly = null;
  },
  { flush: 'sync' }
);
const pagination = ref({ page: 1, rowsPerPage: 20, rowsNumber: 0, sortBy: 'updatedAt', descending: true });
const columns = [
  { name: 'jobStatus', label: '状态', field: 'job_status', align: 'left' },
  { name: 'name', label: '媒体', field: 'video_name', align: 'left', sortable: true },
  { name: 'priority', label: '优先级', field: 'task_priority', align: 'left', sortable: true },
  {
    name: 'nextAttemptAt',
    label: '重试诊断',
    field: (row) => row.retry?.next_attempt_at,
    align: 'left',
    sortable: true,
  },
  { name: 'updatedAt', label: '更新时间', field: 'update_time', align: 'left', sortable: true },
  { name: 'actions', label: '操作', align: 'left' },
];
const statusOptions = JOB_STATUS_OPTIONS;
const videoTypeOptions = Object.entries(VIDEO_TYPE_NAME_MAP).map(([value, label]) => ({ label, value: Number(value) }));
const errorOptions = Object.entries(ERROR_CATEGORY_META)
  .filter(([key]) => key !== 'NONE')
  .map(([value, meta]) => ({ value, label: meta.label }));
const priorityOptions = [
  { label: '高（P0–P3）', value: 'high' },
  { label: '中（P4–P6）', value: 'middle' },
  { label: '低（P7–P10）', value: 'low' },
];
const queueStateOptions = [
  { label: '可立即处理', value: 'ready' },
  { label: '退避等待中', value: 'backoff_waiting' },
  { label: '正在下载', value: 'downloading' },
  { label: '集号回退就绪', value: 'numbering_ready' },
  { label: '元数据阻塞', value: 'metadata_blocked' },
];
const formatTime = (value) => (value ? dayjs(value).format('MM-DD HH:mm') : '—');
let requestSequence = 0;
const summaryMetrics = computed(() => [
  {
    label: '任务总数',
    value: summary.total,
    note: '当前完整队列',
    clearAll: true,
    active: !Object.values(filters).some((value) => value !== null && value !== ''),
  },
  {
    label: '正在下载',
    value: summary.downloading || 0,
    note: '当前落盘任务',
    queueState: 'downloading',
    active: filters.queueState === 'downloading',
    className: 'text-primary',
  },
  {
    label: '可立即处理',
    value: summary.ready_now,
    note: summary.oldest_ready_at ? `最早 ${formatTime(summary.oldest_ready_at)}` : '等待且已到执行时间',
    queueState: 'ready',
    active: filters.queueState === 'ready',
    className: 'text-primary',
  },
  {
    label: '退避等待中',
    value: summary.backoff_waiting || 0,
    note: summary.earliest_retry_at ? `最早 ${formatTime(summary.earliest_retry_at)}` : '退避调度中',
    queueState: 'backoff_waiting',
    active: filters.queueState === 'backoff_waiting',
    className: 'text-warning',
  },
  {
    label: '未命中字幕',
    value: summary.actionable_by_error_category?.NO_SUBTITLE || 0,
    note: '仅统计仍需处置任务',
    errorCategory: 'NO_SUBTITLE',
    actionableOnly: true,
    active: filters.errorCategory === 'NO_SUBTITLE' && filters.actionableOnly === true,
    className: 'text-negative',
  },
  {
    label: '集号回退就绪',
    value: summary.numbering_ready || 0,
    note: `等待剧集 ${summary.episode_waiting || 0} 项`,
    queueState: 'numbering_ready',
    active: filters.queueState === 'numbering_ready',
    className: 'text-primary',
  },
  {
    label: '元数据阻塞',
    value: summary.metadata_blocked || 0,
    note: '缺少剧集 NFO 或有效根目录',
    queueState: 'metadata_blocked',
    active: filters.queueState === 'metadata_blocked',
    className: summary.metadata_blocked ? 'text-warning' : 'text-positive',
  },
]);

const isStale = computed(() => !generatedAt.value || clock.value - dayjs(generatedAt.value).valueOf() > 45000);
const refreshStatus = computed(() => {
  if (!generatedAt.value) return '等待首次更新';
  const label = dayjs(generatedAt.value).format('HH:mm:ss');
  return isStale.value ? `数据可能已陈旧 · ${label}` : `更新于 ${label} · 15 秒自动刷新`;
});
const queueStateLabel = (value) => {
  if (value === 'retry_scheduled') return '已安排重试（兼容视图）';
  return queueStateOptions.find((item) => item.value === value)?.label || value;
};
const clearFilters = () =>
  Object.assign(filters, {
    search: '',
    status: null,
    videoType: null,
    errorCategory: null,
    actionableOnly: null,
    queueState: null,
    priority: null,
  });
const applyMetricFilter = (metric) => {
  clearFilters();
  if (metric.clearAll) {
    return;
  }
  filters.queueState = metric.queueState || null;
  filters.errorCategory = metric.errorCategory || null;
  filters.actionableOnly = metric.actionableOnly || null;
};

const load = async (silent = false) => {
  requestSequence += 1;
  const sequence = requestSequence;
  clock.value = Date.now();
  if (!silent) loading.value = true;
  const p = pagination.value;
  const params = {
    page: p.page,
    pageSize: p.rowsPerPage,
    sortBy: p.sortBy || 'updatedAt',
    sortOrder: p.descending ? 'desc' : 'asc',
  };
  Object.entries(filters).forEach(([key, value]) => {
    if (key === 'actionableOnly' && !filters.errorCategory) return;
    if (value !== null && value !== '') params[key] = value;
  });
  const [res, err] = await JobApi.getPage(params);
  if (sequence !== requestSequence) return;
  loading.value = false;
  if (err) {
    loadError.value = err.message || '无法读取下载队列';
    return;
  }
  loadError.value = '';
  rows.value = res.data || [];
  Object.assign(summary, res.summary || {});
  pagination.value.rowsNumber = res.pagination?.total_items || 0;
  generatedAt.value = res.generated_at || new Date().toISOString();
  const refreshedRows = new Map(rows.value.map((item) => [item.id, item]));
  // Selection is page-scoped. Never retain hidden rows after a filter/page
  // change, otherwise a later batch action could mutate an invisible task.
  selected.value = selected.value.map((item) => refreshedRows.get(item.id)).filter(Boolean);
};
const onRequest = ({ pagination: next }) => {
  pagination.value = next;
  load();
};
watch(
  filters,
  () => {
    pagination.value.page = 1;
    load();
  },
  { deep: true }
);
watch(
  () => route.query,
  (query) => Object.assign(filters, filtersFromRoute(query)),
  { deep: true }
);
const errorMeta = (category) => ERROR_CATEGORY_META[category] || ERROR_CATEGORY_META.UNKNOWN;
const retryText = (row) => {
  if (row.retry?.is_forced) return '已手动插队';
  if (!row.retry?.is_scheduled) return row.job_status === JOB_STATUS_PENDING ? '可立即执行' : '—';
  if (row.retry?.is_ready) return '退避已结束';
  const seconds = Math.max(0, row.retry?.retry_in_seconds || 0);
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟后`;
  return `${Math.ceil(seconds / 3600)} 小时后`;
};
const identityText = (row) =>
  row.absolute_episode ? `绝对集 E${row.absolute_episode}` : `S${row.season}E${row.episode}`;
const mutateSelected = async (payloadFor) => {
  const targets = [...selected.value];
  let cursor = 0;
  let errors = 0;
  const worker = async () => {
    if (cursor < targets.length) {
      const index = cursor;
      cursor += 1;
      const [, err] = await JobApi.update(targets[index].id, payloadFor(targets[index]));
      if (err) errors += 1;
      await worker();
    }
  };
  const workerCount = Math.min(6, targets.length);
  await Promise.all(Array.from({ length: workerCount }, () => worker()));
  if (errors) SystemMessage.error(`${errors} 个任务修改失败`);
  else SystemMessage.success('任务已更新');
  selected.value = [];
  load();
};
const batchUpdatePriority = (priority) =>
  $q
    .dialog({ title: '确认修改所选任务优先级？', cancel: true })
    .onOk(() => mutateSelected(() => ({ task_priority: priority })));
const batchUpdateStatus = () =>
  $q
    .dialog({
      title: '修改状态',
      options: {
        type: 'radio',
        items: [
          { label: JOB_STATUS_MAP[JOB_STATUS_PENDING], value: JOB_STATUS_PENDING },
          { label: JOB_STATUS_MAP[JOB_STATUS_IGNORE], value: JOB_STATUS_IGNORE },
        ],
      },
      cancel: true,
    })
    .onOk((status) => mutateSelected(() => ({ job_status: status })));

useInterval(() => load(true), 15000);
</script>

<style scoped>
.job-name-cell {
  max-width: 420px;
}
.sticky-action {
  white-space: nowrap;
}
.metric-action {
  min-width: 0;
  color: inherit;
  cursor: pointer;
  text-align: left;
}
.metric-action:focus-visible {
  border-radius: var(--csf-radius-lg);
  outline: 3px solid rgba(43, 126, 93, 0.24);
  outline-offset: 3px;
}
.metric-card {
  transition: border-color 120ms ease, background-color 120ms ease;
}
.metric-action:hover,
.metric-card--active {
  border-color: var(--csf-primary) !important;
  background: var(--csf-primary-soft);
}
.refresh-status {
  white-space: nowrap;
}
</style>
