<template>
  <q-page class="app-page settings-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">SYSTEM CONFIGURATION</div>
        <h1>系统设置</h1>
        <p>按工作流组织扫描、匹配、字幕源和外部服务配置；少数需要重启的底层选项会单独标注。</p>
      </div>
      <q-btn flat icon="monitor_heart" label="查看运行状态" to="/suppliers" />
    </section>

    <div v-if="isJobRunning" class="status-strip status-strip--warning q-mb-md" role="status">
      <q-icon name="lock_clock" size="21px" />
      <div class="col">
        <div class="text-weight-medium">自动任务运行期间配置为只读</div>
        <div class="text-caption">先停止自动任务，再修改或保存设置；运行状态和已有任务不会丢失。</div>
      </div>
      <q-btn flat dense no-caps label="前往总览" to="/overview" />
    </div>

    <q-card v-if="settingsLoading" flat class="settings-shell">
      <div class="settings-nav"><q-skeleton v-for="n in 6" :key="n" type="rect" height="48px" class="q-mb-sm" /></div>
      <div class="q-pa-lg">
        <q-skeleton type="text" width="38%" /><q-skeleton type="rect" height="360px" class="q-mt-lg" />
      </div>
    </q-card>

    <q-card v-else-if="settingsError" flat class="surface-card empty-state">
      <div>
        <q-icon name="cloud_off" size="42px" class="empty-state__icon" />
        <div class="empty-state__title">设置加载失败</div>
        <div class="q-mb-md">{{ settingsError }}</div>
        <q-btn outline color="primary" label="重新加载" @click="reloadSettings" />
      </div>
    </q-card>

    <q-card v-else-if="isSettingsLoaded" flat class="settings-shell">
      <nav class="settings-nav" aria-label="设置分类">
        <q-item
          v-for="item in panels"
          :key="item.name"
          clickable
          :class="['settings-nav__item', { 'is-active': tab === item.name }]"
          :aria-current="tab === item.name ? 'page' : undefined"
          @click="selectPanel(item.name)"
        >
          <q-item-section avatar><q-icon :name="item.icon" /></q-item-section>
          <q-item-section>
            <q-item-label>{{ item.label }}</q-item-label>
            <q-item-label caption>{{ item.short }}</q-item-label>
          </q-item-section>
        </q-item>
      </nav>

      <q-form class="settings-content" @submit="submitAll">
        <header class="settings-content__header">
          <h2>{{ activePanel.label }}</h2>
          <p>{{ activePanel.description }}</p>
        </header>

        <q-tab-panels v-model="tab" animated :class="{ 'settings-locked': isJobRunning }" :inert="isJobRunning">
          <q-tab-panel name="basic" class="q-pa-none"><basic-settings /></q-tab-panel>
          <q-tab-panel name="advanced" class="q-pa-none"><advanced-settings /></q-tab-panel>
          <q-tab-panel name="subSource" class="q-pa-none"><sub-source-settings /></q-tab-panel>
          <q-tab-panel name="ai" class="q-pa-none"><ai-settings /></q-tab-panel>
          <q-tab-panel name="emby" class="q-pa-none"><emby-settings /></q-tab-panel>
          <q-tab-panel name="experiment" class="q-pa-none"><experiment-settings /></q-tab-panel>
          <q-tab-panel name="development" class="q-pa-none"><development-settings /></q-tab-panel>
        </q-tab-panels>

        <form-submit-area />
      </q-form>
    </q-card>
  </q-page>
</template>

<script setup>
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import BasicSettings from 'pages/settings/SettingsPanelBasic';
import AdvancedSettings from 'pages/settings/SettingsPanelAdvanced';
import EmbySettings from 'pages/settings/SettingsPanelEmby';
import DevelopmentSettings from 'pages/settings/SettingsPanelDevelopment';
import ExperimentSettings from 'pages/settings/SettingsPanelExperiment';
import FormSubmitArea from 'pages/settings/FormSubmitArea';
import SubSourceSettings from 'pages/settings/SettingsPanelSubSource';
import AiSettings from 'pages/settings/SettingsPanelAI';
import {
  formModel,
  reloadSettings,
  settingsError,
  settingsLoading,
  submitAll,
  useSettings,
} from 'pages/settings/use-settings';
import { isJobRunning } from 'src/store/systemState';

const panels = [
  {
    name: 'basic',
    label: '基础与扫描',
    short: '目录、周期',
    icon: 'schedule',
    description: '设置媒体目录、扫描周期和下载线程。',
  },
  {
    name: 'advanced',
    label: '匹配与队列',
    short: '规则、重试',
    icon: 'rule',
    description: '调整字幕命名、匹配策略、队列退避和网络选项。',
  },
  {
    name: 'subSource',
    label: '字幕源与凭据',
    short: '地址、密钥',
    icon: 'source',
    description: '集中管理所有字幕源地址、启用状态、配额与访问凭据。',
  },
  {
    name: 'ai',
    label: 'AI 歧义识别',
    short: '模型、阈值',
    icon: 'psychology_alt',
    description: '只在确定性规则无法区分候选时，让兼容模型提供受约束的辅助判断。',
  },
  {
    name: 'emby',
    label: '媒体服务器',
    short: 'Emby 连接',
    icon: 'dns',
    description: '配置媒体服务器连接、路径映射和观看状态过滤。',
  },
  {
    name: 'experiment',
    label: '转换与接口',
    short: '编码、API',
    icon: 'science',
    description: '管理字幕编码转换、远程浏览器和本地 API 访问。',
  },
  {
    name: 'development',
    label: '维护通知',
    short: '开发者选项',
    icon: 'build_circle',
    description: '面向项目维护者的接口失效通知。普通使用无需配置。',
  },
];

const route = useRoute();
const router = useRouter();
const validPanel = (value) => panels.some((item) => item.name === value);
const tab = ref(validPanel(route.query.tab) ? route.query.tab : 'basic');
const activePanel = computed(() => panels.find((item) => item.name === tab.value) || panels[0]);
const isSettingsLoaded = computed(() => Object.keys(formModel).length > 0);

const selectPanel = (name) => {
  tab.value = name;
  router.replace({ query: { ...route.query, tab: name } });
};

watch(
  () => route.query.tab,
  (value) => {
    if (validPanel(value)) tab.value = value;
  }
);

useSettings();
</script>
