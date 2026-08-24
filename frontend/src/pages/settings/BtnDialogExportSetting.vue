<template>
  <q-btn outline color="grey-8" icon="file_download" label="导出" @click="visible = true" />
  <q-dialog v-model="visible">
    <q-card class="wide-dialog">
      <q-card-section class="dialog-header row items-center">
        <div>
          <div class="eyebrow">EXPORT SETTINGS</div>
          <div class="text-h6">导出配置</div>
        </div>
        <q-space /><q-btn v-close-popup flat round dense icon="close" aria-label="关闭" />
      </q-card-section>

      <q-separator />

      <q-card-section>
        <q-toggle v-model="hideSensitive" color="primary" label="移除账号、地址和密钥等敏感配置" />
        <div class="code-area relative-position overflow-auto bg-grey-2 q-pa-sm">
          <copy-to-clipboard-btn class="copy-btn absolute-top-right q-ma-sm hidden" :text="settingsString" />
          <pre class="settings-export-preview">{{ settingsString }}</pre>
        </div>
      </q-card-section>

      <q-separator />

      <q-card-actions align="right" class="q-pa-md">
        <q-btn class="q-px-md" v-close-popup flat color="primary" label="关闭" />
        <q-btn class="q-px-md" unelevated color="primary" icon="download" label="导出 JSON" @click="exportSettings" />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<script setup>
import { computed, ref } from 'vue';
import { getExportSettings } from 'pages/settings/use-settings';
import { saveText } from 'src/utils/file-download';
import CopyToClipboardBtn from 'components/CopyToClipboardBtn';

const visible = ref(false);
const hideSensitive = ref(true);

const settingsString = computed(() => {
  const settings = getExportSettings(!hideSensitive.value);
  return JSON.stringify(settings, null, 2);
});

const exportSettings = () => {
  saveText('ChineseSubFinderSettings.json', settingsString.value);
};
</script>

<style lang="scss" scoped>
.code-area:hover {
  .copy-btn {
    display: block !important;
  }
}
</style>
