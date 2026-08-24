<template>
  <q-dialog ref="dialogRef" persistent transition-duration="160">
    <q-card class="prepare-dialog">
      <q-card-section class="dialog-header row items-center">
        <div>
          <div class="eyebrow">APPLYING CONFIGURATION</div>
          <div class="text-h6">正在准备服务</div>
        </div>
        <q-space /><q-badge :color="hasError ? 'negative' : isDone ? 'positive' : 'primary'">{{
          hasError ? '需要处理' : isDone ? '已完成' : '进行中'
        }}</q-badge>
      </q-card-section>
      <q-linear-progress v-if="!isDone && !hasError" indeterminate color="primary" />
      <q-separator v-else />

      <q-card-section class="q-pa-lg">
        <div class="row items-start q-gutter-md">
          <q-spinner v-if="!isDone && !hasError" color="primary" size="30px" />
          <q-icon
            v-else
            :name="hasError ? 'error_outline' : 'check_circle'"
            :color="hasError ? 'negative' : 'positive'"
            size="30px"
          />
          <div class="col">
            <div class="section-title">{{ prejobStatus.stage_name || '初始化运行环境' }}</div>
            <div class="text-grey-7 q-mt-xs">{{ prejobStatus.now_process_info || '正在读取当前阶段…' }}</div>
          </div>
        </div>
      </q-card-section>

      <template v-if="hasError">
        <q-separator />
        <q-card-section class="prepare-dialog__errors">
          <div class="text-weight-medium q-mb-sm">错误详情</div>
          <pre v-if="prejobStatus.g_error_info">{{ prejobStatus.g_error_info }}</pre>
          <template v-if="prejobStatus?.rename_err_results?.length">
            <div class="text-weight-medium q-mt-md q-mb-sm">无法重命名的字幕文件</div>
            <pre>{{ prejobStatus.rename_err_results.join('\n') }}</pre>
          </template>
        </q-card-section>
        <q-card-actions align="right" class="q-pa-md"
          ><q-btn v-close-popup unelevated color="primary" label="关闭并检查配置"
        /></q-card-actions>
      </template>
    </q-card>
  </q-dialog>
</template>

<script setup>
import { computed, watch } from 'vue';
import { useDialogPluginComponent } from 'quasar';
import { systemState } from 'src/store/systemState';

const { dialogRef } = useDialogPluginComponent();
const prejobStatus = computed(() => systemState.preJobStatus || {});
const isDone = computed(() => !!prejobStatus.value.is_done);
const hasError = computed(() => !!prejobStatus.value.g_error_info || prejobStatus.value.rename_err_results?.length > 0);

watch(isDone, (done) => {
  if (done && !hasError.value) dialogRef.value?.hide();
});
</script>

<style scoped>
.prepare-dialog {
  width: 680px;
}
.prepare-dialog__errors {
  max-height: 50dvh;
  overflow: auto;
}
pre {
  margin: 0;
  padding: 12px;
  overflow: auto;
  border-radius: 8px;
  background: #f4f6f5;
  font: 12px/1.6 ui-monospace, monospace;
  white-space: pre-wrap;
}
</style>
