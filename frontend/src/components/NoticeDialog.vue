<template>
  <q-dialog v-model="visible" @hide="rememberNotice">
    <q-card class="notice-dialog">
      <q-card-section class="row items-start">
        <div>
          <div class="eyebrow">WHAT'S NEW</div>
          <div class="text-h6">本版本更新</div>
        </div>
        <q-space />
        <q-btn flat round dense icon="close" aria-label="关闭更新说明" @click="handleAgree" />
      </q-card-section>

      <q-separator />

      <q-card-section class="notice-dialog__content">
        <markdown :source="notifyContent"></markdown>
      </q-card-section>

      <q-separator />

      <q-card-actions align="right" class="q-pa-md">
        <q-btn class="q-px-md" unelevated color="primary" label="开始使用" @click="handleAgree" />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue';
import { LocalStorage } from 'quasar';
import { until } from '@vueuse/core';
import { systemState } from 'src/store/systemState';
// eslint-disable-next-line import/no-webpack-loader-syntax
import notifyContent from 'raw-loader!../../NOTIFY.md';
import Markdown from 'components/Markdown';

const visible = ref(false);

const currentVersion = computed(() => systemState.systemInfo?.version);

const noticeFlagItemKey = computed(() => `noticeFlag-${currentVersion.value}`);

const handleAgree = () => {
  visible.value = false;
};

const rememberNotice = () => {
  LocalStorage.set(noticeFlagItemKey.value, true);
};

onMounted(async () => {
  await until(() => currentVersion.value !== undefined).toBe(true);
  const noticeFlag = LocalStorage.getItem(noticeFlagItemKey.value);
  if (!noticeFlag) {
    visible.value = true;
  }
});
</script>

<style scoped>
.notice-dialog {
  width: 760px;
}
.notice-dialog__content {
  max-height: min(68vh, 680px);
  overflow: auto;
}
</style>
