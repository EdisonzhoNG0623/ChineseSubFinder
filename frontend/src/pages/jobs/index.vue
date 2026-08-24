<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">DOWNLOAD QUEUE</div>
        <h1>下载队列</h1>
        <p>服务端筛选与分页，重试时间和失败原因可直接诊断。</p>
      </div>
      <q-btn flat round icon="refresh" :loading="loading" @click="load" title="刷新" />
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
      <div v-for="metric in summaryMetrics" :key="metric.label">
        <q-card flat bordered class="metric-card"
          ><q-card-section
            ><div class="metric-label">{{ metric.label }}</div>
            <div class="metric-value" :class="metric.className">{{ metric.value }}</div>
            <div class="metric-note">{{ metric.note }}</div></q-card-section
          ></q-card
        >
      </div>
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

      <q-separator />
      <q-card-section v-if="selected.length" class="row items-center q-gutter-sm bg-blue-grey-1">
        <span class="text-weight-medium">已选 {{ selected.length }} 项</span>
        <q-btn
          size="sm"
          outline
          color="primary"
          icon="expand_less"
          label="提高优先级"
          @click="batchUpdatePriority('high')"
        />
        <q-btn
          size="sm"
          outline
          color="primary"
          icon="expand_more"
          label="降低优先级"
          @click="batchUpdatePriority('low')"
        />
        <q-btn size="sm" outline color="primary" label="修改状态" @click="batchUpdateStatus" />
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
import JobApi from 'src/api/JobApi';
import JobLogBtnDialog from 'pages/jobs/JobLogBtnDialog';
import JobDetailBtnDialog from 'pages/jobs/JobDetailBtnDialog';
import { SystemMessage } from 'src/utils/message';
import { VIDEO_TYPE_NAME_MAP } from 'src/constants/SettingConstants';
import {
  ERROR_CATEGORY_META,
  JOB_STATUS_COLOR_MAP,
  JOB_STATUS_IGNORE,
  JOB_STATUS_MAP,
  JOB_STATUS_OPTIONS,
  JOB_STATUS_PENDING,
} from 'src/constants/JobConstants';

const $q = useQuasar();
const rows = ref([]);
const selected = ref([]);
const loading = ref(false);
const loadError = ref('');
const summary = reactive({ total: 0, by_status: {}, by_error_category: {}, retry_scheduled: 0, ready_now: 0 });
const filters = reactive({ search: '', status: null, videoType: null, errorCategory: null, priority: null });
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
let requestSequence = 0;
const summaryMetrics = computed(() => [
  { label: '任务总数', value: summary.total, note: '当前完整队列' },
  { label: '可立即处理', value: summary.ready_now, note: '等待且已到执行时间', className: 'text-primary' },
  { label: '等待重试', value: summary.retry_scheduled, note: '退避调度中', className: 'text-warning' },
  {
    label: '未命中字幕',
    value: summary.by_error_category?.NO_SUBTITLE || 0,
    note: '建议检查源与识别结果',
    className: 'text-negative',
  },
]);

const load = async () => {
  requestSequence += 1;
  const sequence = requestSequence;
  loading.value = true;
  const p = pagination.value;
  const params = {
    page: p.page,
    pageSize: p.rowsPerPage,
    sortBy: p.sortBy || 'updatedAt',
    sortOrder: p.descending ? 'desc' : 'asc',
  };
  Object.entries(filters).forEach(([key, value]) => {
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
  selected.value = [];
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
const formatTime = (value) => (value ? dayjs(value).format('MM-DD HH:mm') : '—');
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
  const results = await Promise.all(selected.value.map((item) => JobApi.update(item.id, payloadFor(item))));
  const errors = results.filter(([, err]) => err).length;
  if (errors) SystemMessage.error(`${errors} 个任务修改失败`);
  else SystemMessage.success('任务已更新');
  load();
};
const priorityLabel = (priority) => {
  if (priority <= 3) return '高';
  if (priority >= 7) return '低';
  return '中';
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
    .onOk((status) =>
      mutateSelected((item) => ({
        job_status: status,
        task_priority: priorityLabel(item.task_priority),
      }))
    );

load();
</script>

<style scoped>
.job-name-cell {
  max-width: 420px;
}
.sticky-action {
  white-space: nowrap;
}
</style>
