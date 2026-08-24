<template>
  <q-btn label="任务日志" flat dense @click="show = true" color="primary" />

  <q-dialog v-model="show" @before-show="handleBeforeShow">
    <q-card class="wide-dialog">
      <q-card-section class="dialog-header row items-center">
        <div>
          <div class="eyebrow">TASK LOG</div>
          <div class="text-h6">任务日志</div>
        </div>
        <q-space /><q-btn v-close-popup flat round dense icon="close" aria-label="关闭" />
      </q-card-section>

      <q-separator />

      <q-card-section class="dialog-scroll-area">
        <log-viewer-raw v-if="logLines?.length" :log-lines="logLines" class="fit" />
        <div v-else class="empty-state">该任务还没有可显示的日志。</div>
      </q-card-section>
    </q-card>
  </q-dialog>
</template>

<script setup>
import JobApi from 'src/api/JobApi';
import { ref } from 'vue';
import { SystemMessage } from 'src/utils/message';
import LogViewerRaw from 'components/LogViewerRaw';

const props = defineProps({
  data: {
    type: Object,
  },
});

const show = ref(false);
const logLines = ref([]);

const getJobLog = async () => {
  const [res, err] = await JobApi.getLog(props.data.id);
  if (err != null) {
    SystemMessage.error(err.message);
  } else {
    logLines.value = res?.one_line;
  }
};

const handleBeforeShow = () => {
  getJobLog();
};
</script>
