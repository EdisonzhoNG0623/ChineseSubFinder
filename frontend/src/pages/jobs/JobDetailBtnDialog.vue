<template>
  <q-btn label="详情" flat dense @click="show = true" color="primary" />
  <q-dialog v-model="show">
    <q-card class="job-dialog">
      <q-card-section class="row items-start">
        <div class="col">
          <div class="text-overline text-primary">TASK DIAGNOSTICS</div>
          <div class="text-h6 ellipsis">{{ data.video_name }}</div>
          <div class="text-caption text-grey-7">
            {{ VIDEO_TYPE_NAME_MAP[data.video_type] }} · P{{ data.task_priority }} ·
            {{ JOB_STATUS_MAP[data.job_status] }}
          </div>
        </div>
        <q-btn flat round dense icon="close" v-close-popup />
      </q-card-section>
      <q-tabs v-model="tab" dense align="left" active-color="primary" indicator-color="primary">
        <q-tab name="overview" label="任务" />
        <q-tab name="identity" label="集号识别" :disable="!isEpisode" />
        <q-tab name="retry" label="重试诊断" />
      </q-tabs>
      <q-separator />
      <q-tab-panels v-model="tab" animated>
        <q-tab-panel name="overview">
          <div class="detail-grid">
            <div>
              <span>任务 ID</span><strong>{{ data.id }}</strong>
            </div>
            <div>
              <span>媒体服务器 ID</span><strong>{{ data.media_server_inside_video_id || '—' }}</strong>
            </div>
            <div>
              <span>下载 / 重试</span><strong>{{ data.download_times }} / {{ data.retry_times }}</strong>
            </div>
            <div>
              <span>加入时间</span><strong>{{ formatTime(data.added_time) }}</strong>
            </div>
            <div>
              <span>更新时间</span><strong>{{ formatTime(data.update_time) }}</strong>
            </div>
            <div>
              <span>特征码</span><strong class="ellipsis">{{ data.feature || '—' }}</strong>
            </div>
          </div>
          <q-separator spaced />
          <div class="text-caption text-grey-7">视频路径</div>
          <div class="path-value">{{ data.video_f_path }}</div>
          <template v-if="data.error_info"
            ><div class="text-caption text-negative q-mt-md">最近错误</div>
            <q-banner rounded class="bg-red-1 text-negative q-mt-xs">{{ data.error_info }}</q-banner></template
          >
        </q-tab-panel>

        <q-tab-panel name="identity">
          <div class="row q-col-gutter-md q-mb-md">
            <div class="col-4">
              <div class="identity-number">S{{ pad(data.season) }}E{{ pad(data.episode) }}</div>
              <div class="text-caption">标准集号</div>
            </div>
            <div class="col-4">
              <div class="identity-number">{{ data.absolute_episode ? `E${data.absolute_episode}` : '—' }}</div>
              <div class="text-caption">绝对集号</div>
            </div>
            <div class="col-4">
              <div class="identity-number">
                {{ data.scene_season ? `S${pad(data.scene_season)}E${pad(data.scene_episode)}` : '—' }}
              </div>
              <div class="text-caption">Scene 集号</div>
            </div>
          </div>
          <q-list bordered separator>
            <q-item
              ><q-item-section
                ><q-item-label caption>识别来源</q-item-label
                ><q-item-label>{{ data.numbering_source || '仅本地标准集号' }}</q-item-label></q-item-section
              ><q-item-section side>{{ confidence }}</q-item-section></q-item
            >
            <q-item v-for="query in data.identity?.query_plan || []" :key="`${query.kind}-${query.query}`"
              ><q-item-section avatar
                ><q-badge
                  outline
                  :color="query.kind === 'ABSOLUTE' ? 'purple' : query.kind === 'SCENE' ? 'orange' : 'primary'"
                  >{{ query.kind }}</q-badge
                ></q-item-section
              ><q-item-section>{{ query.query }}</q-item-section></q-item
            >
          </q-list>
          <div v-if="!data.identity?.query_plan?.length" class="empty-state">
            当前任务还没有可展示的回退搜索计划；旧任务会在下一次处理时自动回填。
          </div>
        </q-tab-panel>

        <q-tab-panel name="retry">
          <div class="row items-center q-gutter-md q-mb-md">
            <q-icon name="schedule" size="36px" color="primary" />
            <div>
              <div class="text-h6">{{ retryHeadline }}</div>
              <div class="text-caption text-grey-7">{{ retryDetail }}</div>
            </div>
          </div>
          <q-list bordered separator>
            <q-item
              ><q-item-section
                ><q-item-label caption>错误类别</q-item-label
                ><q-item-label
                  ><q-badge :color="errorMeta.color" outline>{{ errorMeta.label }}</q-badge></q-item-label
                ></q-item-section
              ></q-item
            >
            <q-item
              ><q-item-section
                ><q-item-label caption>下次尝试</q-item-label
                ><q-item-label>{{ formatTime(data.retry?.next_attempt_at) }}</q-item-label></q-item-section
              ></q-item
            >
            <q-item
              ><q-item-section
                ><q-item-label caption>是否手动插队</q-item-label
                ><q-item-label>{{
                  data.retry?.is_forced ? '是，将在下一轮优先处理' : '否'
                }}</q-item-label></q-item-section
              ></q-item
            >
          </q-list>
        </q-tab-panel>
      </q-tab-panels>
    </q-card>
  </q-dialog>
</template>

<script setup>
import { computed, ref } from 'vue';
import dayjs from 'dayjs';
import { ERROR_CATEGORY_META, JOB_STATUS_MAP } from 'src/constants/JobConstants';
import { VIDEO_TYPE_MOVIE, VIDEO_TYPE_NAME_MAP } from 'src/constants/SettingConstants';

const props = defineProps({ data: { type: Object, required: true } });
const show = ref(false);
const tab = ref('overview');
const isEpisode = computed(() => props.data.video_type !== VIDEO_TYPE_MOVIE);
const errorMeta = computed(() => ERROR_CATEGORY_META[props.data.retry?.category] || ERROR_CATEGORY_META.UNKNOWN);
const confidence = computed(() =>
  props.data.numbering_confidence ? `${Math.round(props.data.numbering_confidence * 100)}% 置信度` : '—'
);
const retryHeadline = computed(() => {
  if (props.data.retry?.is_forced) return '任务已手动插队';
  if (props.data.retry?.is_ready) return '当前可以重试';
  if (props.data.retry?.is_scheduled) return '任务正在退避等待';
  return '没有已安排的重试';
});
const retryDetail = computed(() =>
  props.data.retry?.is_scheduled
    ? `约 ${Math.ceil((props.data.retry.retry_in_seconds || 0) / 60)} 分钟后可执行`
    : '状态变化后会自动更新调度'
);
const formatTime = (value) =>
  value && !String(value).startsWith('0001-') ? dayjs(value).format('YYYY-MM-DD HH:mm:ss') : '—';
const pad = (value) => String(value || 0).padStart(2, '0');
</script>

<style scoped>
.job-dialog {
  width: 880px;
  max-width: 94vw;
}
.detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 18px;
}
.detail-grid div {
  min-width: 0;
}
.detail-grid span {
  display: block;
  color: #7a8190;
  font-size: 12px;
}
.detail-grid strong {
  display: block;
  margin-top: 4px;
  font-weight: 600;
}
.path-value {
  font-family: ui-monospace, monospace;
  overflow-wrap: anywhere;
}
.identity-number {
  color: #3348c7;
  font-size: 24px;
  font-weight: 700;
}
@media (max-width: 600px) {
  .detail-grid {
    grid-template-columns: 1fr;
  }
}
</style>
