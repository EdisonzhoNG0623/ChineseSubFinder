<template>
  <span v-if="hasNewVersion" @click="visible = true">
    <slot v-if="$slots.default"></slot>
    <q-badge v-else class="cursor-pointer" label="有更新" title="有新的版本更新" />
  </span>
  <q-dialog v-if="latestVersion" v-model="visible">
    <q-card class="column update-dialog">
      <q-card-section class="dialog-header row items-center">
        <div>
          <div class="eyebrow">VERSION UPDATE</div>
          <div class="text-h5">{{ latestVersion.tag_name }}</div>
        </div>
        <q-space /><q-btn v-close-popup flat round dense icon="close" aria-label="关闭" />
      </q-card-section>

      <q-tabs
        v-model="tab"
        dense
        class="text-grey"
        active-color="primary"
        indicator-color="primary"
        align="justify"
        narrow-indicator
      >
        <q-tab name="log" label="更新日志" />
        <q-tab name="update" label="升级方式" />
      </q-tabs>

      <q-separator />

      <q-tab-panels class="col" v-model="tab" animated>
        <q-tab-panel name="log">
          <markdown :source="latestVersion.body" />
        </q-tab-panel>
        <q-tab-panel name="update">
          <section class="q-mb-lg">
            <div class="text-h6">Docker / Unraid</div>
            <p>更新镜像后重新创建容器，保留当前配置目录映射。自定义构建版本请以维护者发布说明为准。</p>
            <div>
              查看
              <!-- eslint-disable-next-line max-len -->
              <a
                href="https://github.com/ChineseSubFinder/ChineseSubFinder/blob/master/docker/readme.md"
                target="_blank"
                rel="noopener noreferrer"
              >
                Docker 部署说明
              </a>
            </div>
            <div class="text-caption text-grey-7">镜像发布通常晚于 GitHub Release，请以镜像仓库标签为准。</div>
          </section>
          <section>
            <div class="text-h6">Windows</div>
            <p>下载对应 Release，在备份配置后替换程序文件。</p>
          </section>
        </q-tab-panel>
      </q-tab-panels>

      <q-separator />

      <q-card-actions align="right">
        <q-btn unelevated color="primary" icon-right="open_in_new" @click="navigateToReleasePage">查看 Release</q-btn>
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue';
import Markdown from 'components/Markdown';
import { systemState } from 'src/store/systemState';
import { LocalStorage } from 'quasar';

const latestVersion = ref(LocalStorage.getItem('latestVersion') ?? null);
const visible = ref(false);
const tab = ref('log');

const parseVersion = (value) => {
  const match = String(value || '').match(/(\d+)\.(\d+)(?:\.(\d+))?/);
  return match ? match.slice(1).map((part) => Number(part || 0)) : null;
};

const hasNewVersion = computed(() => {
  const current = parseVersion(systemState.systemInfo?.version);
  const latest = parseVersion(latestVersion.value?.tag_name);
  if (!current || !latest) return false;
  for (let index = 0; index < Math.max(current.length, latest.length); index += 1) {
    const difference = (latest[index] || 0) - (current[index] || 0);
    if (difference !== 0) return difference > 0;
  }
  return false;
});

const getLatestVersion = async () => {
  try {
    const data = await fetch('https://api.github.com/repos/ChineseSubFinder/ChineseSubFinder/releases/latest').then(
      (res) => {
        if (res.ok) {
          return res.json();
        }
        return Promise.reject(res);
      }
    );
    latestVersion.value = data;
    // 接口请求速率过高有可能403，本地存一份
    LocalStorage.set('latestVersion', data);
  } catch (e) {
    // do nothing
  }
};

const navigateToReleasePage = () => {
  window.open(latestVersion.value.html_url, '_blank', 'noopener,noreferrer');
  visible.value = false;
};

onMounted(getLatestVersion);
</script>

<style lang="scss" scoped>
a {
  color: $primary;
}
.update-dialog {
  width: 680px;
  min-height: min(540px, 80dvh);
}
</style>
