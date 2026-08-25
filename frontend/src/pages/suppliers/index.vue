<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">SUPPLIER OPERATIONS</div>
        <h1>字幕源运行状态</h1>
        <p>连接性、命中情况和当日配额集中展示；检测在后台运行，不会阻塞页面。</p>
      </div>
      <div class="page-heading__actions row q-gutter-sm">
        <q-btn flat icon="settings" label="配置字幕源" to="/settings?tab=subSource" />
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
        :rows="rows"
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
            <div v-if="row.status_message" class="text-caption text-grey-7 q-mt-xs">{{ row.status_message }}</div>
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
            <div class="text-caption text-grey-7">
              {{ row.empty_results }} 次空结果 / {{ row.errors }} 次错误 / {{ row.timeouts }} 次超时
            </div>
            <div v-if="row.circuit_skips" class="text-caption text-warning">
              熔断已跳过 {{ row.circuit_skips }} 次
            </div>
          </q-td>
        </template>
        <template #body-cell-latency="{ row }">
          <q-td>
            <div>{{ row.average_attempt_millis > 0 ? `平均 ${formatDuration(row.average_attempt_millis)}` : '—' }}</div>
            <div class="text-caption text-grey-7">
              P95 {{ formatDuration(row.p95_attempt_millis) }} · 检测 {{ formatDuration(row.latency_millis) }}
            </div>
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
    <div class="text-caption text-grey-7 q-mt-sm">
      数据每 20 秒刷新。命中、耗时和熔断聚合统计会持久化；不记录媒体路径、候选名称、错误正文或密钥。
    </div>
  </q-page>
</template>

<script setup>
import { computed, ref } from 'vue';
import SupplierApi from 'src/api/SupplierApi';
import useInterval from 'src/composables/use-interval';
import { SystemMessage } from 'src/utils/message';

const rows = ref([]);
const loading = ref(false);
const checking = ref(false);
const loadError = ref('');
const columns = [
  { name: 'name', label: '字幕源', field: 'display_name', align: 'left' },
  { name: 'status', label: '状态', field: 'health', align: 'left' },
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

const headline = computed(() => [
  { label: '已配置', value: rows.value.filter((item) => item.configured).length },
  {
    label: '健康可用',
    value: rows.value.filter((item) => item.health === 'HEALTHY').length,
    className: 'text-positive',
  },
  {
    label: '需关注',
    value: rows.value.filter((item) => ['DEGRADED', 'UNHEALTHY', 'RETIRED'].includes(item.health)).length,
    className: 'text-warning',
  },
  { label: '候选命中', value: rows.value.reduce((sum, item) => sum + item.candidate_hits, 0) },
]);
const usageRatio = (row) => Math.min(1, row.daily_limit > 0 ? row.daily_used / row.daily_limit : 0);
const usageText = (row) =>
  row.daily_limit < 0 ? `${row.daily_used} / 不限` : `${row.daily_used} / ${row.daily_limit}`;
const formatDuration = (millis) => {
  if (!millis) return '—';
  if (millis < 1000) return `${millis} ms`;
  return `${(millis / 1000).toFixed(millis < 10000 ? 1 : 0)} s`;
};

const load = async (silent = false) => {
  if (!silent) loading.value = true;
  const [res, err] = await SupplierApi.getDiagnostics();
  loading.value = false;
  if (err) {
    loadError.value = err.message || '无法读取字幕源状态';
    return;
  }
  loadError.value = '';
  rows.value = res.data || [];
  checking.value = !!res.is_checking;
};
const check = async () => {
  checking.value = true;
  const [, err] = await SupplierApi.check();
  if (err && err.error?.status !== 409) {
    checking.value = false;
    SystemMessage.error(err.message);
    return;
  }
  SystemMessage.success('字幕源检测已启动');
  setTimeout(() => load(true), 1200);
};

useInterval(() => load(rows.value.length > 0), 20000);
</script>
