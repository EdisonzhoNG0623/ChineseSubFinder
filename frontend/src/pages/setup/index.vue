<template>
  <q-page class="setup-page">
    <header class="setup-header">
      <div class="auth-brand"><img src="icons/logo.png" alt="" /><span>ChineseSubFinder</span></div>
      <div class="setup-header__copy">
        <div class="eyebrow">FIRST RUN SETUP</div>
        <h1>完成首次配置</h1>
        <p>依次创建管理员、连接媒体目录并选择媒体服务器。稍后仍可在设置中心修改。</p>
      </div>
    </header>
    <section v-if="setupLoading" class="setup-card q-pa-xl" aria-busy="true">
      <q-skeleton type="text" width="32%" /><q-skeleton type="rect" height="320px" class="q-mt-lg" />
    </section>
    <section v-else-if="setupError" class="setup-card empty-state">
      <div>
        <q-icon name="cloud_off" size="42px" class="empty-state__icon" />
        <div class="empty-state__title">初始化配置加载失败</div>
        <div class="q-mb-md">{{ setupError }}</div>
        <q-btn outline color="primary" label="重试" @click="reloadSetup" />
      </div>
    </section>
    <section v-else class="setup-card">
      <q-stepper v-model="step" ref="stepper" animated vertical flat color="primary">
        <q-step name="1" prefix="1" :done="step > '1'" title="创建管理账号">
          <admin-account-form ref="adminAccountForm" />
        </q-step>

        <q-step name="2" prefix="2" :done="step > '2'" title="配置媒体目录">
          <scan-folder-form ref="scanFolderForm" />
        </q-step>

        <q-step name="3" :done="step > '3'" prefix="3" title="选择媒体服务器">
          <q-form class="q-gutter-md">
            <select-media-server-form />
          </q-form>
        </q-step>

        <q-step v-if="setupState.form.mediaServer === 'emby'" name="31" prefix="4" title="配置 Emby">
          <q-form class="q-gutter-md">
            <emby-setup-form ref="mediaServerSettingForm" />
          </q-form>
        </q-step>

        <template v-slot:navigation>
          <q-stepper-navigation>
            <q-btn
              v-if="showSubmitButton"
              unelevated
              @click="submit"
              :loading="submitting"
              color="primary"
              label="完成配置"
            />
            <q-btn v-else unelevated @click="nextStep" color="primary" label="继续" icon-right="arrow_forward" />
            <q-btn
              v-if="step > '1'"
              flat
              color="grey-8"
              @click="$refs.stepper.previous()"
              label="返回"
              class="q-ml-sm"
            />
          </q-stepper-navigation>
        </template>
      </q-stepper>
    </section>
    <footer class="setup-footer">所有配置都保存在当前实例中。</footer>
  </q-page>
</template>

<script setup>
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import CommonApi from 'src/api/CommonApi';
import { SystemMessage } from 'src/utils/message';
import AdminAccountForm from 'pages/setup/AdminAccountForm';
import ScanFolderForm from 'pages/setup/ScanFolderForm';
import { templateRef } from '@vueuse/core';
import SelectMediaServerForm from 'pages/setup/SelectMediaServerForm';
import { reloadSetup, setupError, setupLoading, setupState, useSetup } from 'pages/setup/use-setup';
import EmbySetupForm from 'pages/setup/EmbySetupForm';
import { deepCopy } from 'src/utils/common';
import { getInfo, isRunningInDocker } from 'src/store/systemState';
import { SUB_NAME_FORMAT_NORMAL } from 'src/constants/SettingConstants';
import { useAppStatusLoading } from 'src/composables/use-app-status-loading';
import { Dialog } from 'quasar';

useSetup();
const { startLoading } = useAppStatusLoading();

const router = useRouter();
const step = ref('1');

const userForm = templateRef('adminAccountForm');
const folderForm = templateRef('scanFolderForm');
const mediaServerSettingForm = templateRef('mediaServerSettingForm');
const stepper = ref(null);
const submitting = ref(false);

const nextStep = async () => {
  let isValid = true;
  if (step.value === '1') {
    isValid = await userForm.value.$refs.form.validate();
  }
  if (step.value === '2') {
    isValid = await folderForm.value.$refs.form.validate();
  }
  if (!isValid) return;
  stepper.value.next();
};

const showSubmitButton = computed(() => {
  if (setupState.form.mediaServer === 'emby') {
    if (step.value === '31') return true;
  } else {
    return step.value === '3';
  }
  return false;
});

const submit = async () => {
  if (isRunningInDocker.value) {
    // 检测电影和连续剧目录是否以 /media 开头
    const isMovieStartsWithMedia = setupState.form.movieFolder.every((item) => item.startsWith('/media'));
    const isSeriesStartsWithMedia = setupState.form.seriesFolder.every((item) => item.startsWith('/media'));
    if (!isMovieStartsWithMedia || !isSeriesStartsWithMedia) {
      Dialog.create({
        title: '请修改相关配置后继续',
        html: true,
        message:
          '软件运行在Docker中，请将电影和电视剧目录修改为 <b>/media</b> 下的目录，否则可能会因为权限问题导致无法正确的加载媒体库',
        persistent: true,
        ok: '确定',
      });
      return;
    }
  }

  let isValid = true;
  if (setupState.form.mediaServer === 'emby') {
    isValid = await mediaServerSettingForm.value.$refs.form.validate();
  }
  if (!isValid) return;
  submitting.value = true;
  const formData = deepCopy(setupState.defaultSettings);
  formData.user_info = {
    username: setupState.form.username,
    password: setupState.form.password,
  };
  formData.common_settings = {
    ...formData.common_settings,
    movie_paths: setupState.form.movieFolder,
    series_paths: setupState.form.seriesFolder,
  };
  if (setupState.form.mediaServer === 'emby') {
    formData.emby_settings = {
      ...formData.emby_settings,
      enable: true,
      address_url: setupState.form.emby.url,
      api_key: setupState.form.emby.apiKey,
      max_request_video_number: setupState.form.emby.limitCount,
      skip_watched: setupState.form.emby.skipWatched,
      auto_or_manual: setupState.form.emby.autoOrManual,
      movie_paths_mapping: setupState.form.emby.movieFolderMap,
      series_paths_mapping: setupState.form.emby.seriesFolderMap,
    };
  } else {
    formData.advanced_settings.sub_name_formatter = SUB_NAME_FORMAT_NORMAL;
  }
  const [, err] = await CommonApi.setup({
    settings: formData,
  });
  submitting.value = false;
  if (err !== null) {
    SystemMessage.error(err.message);
    return;
  }
  SystemMessage.success('初始化完成');
  await getInfo();
  startLoading();
  router.push('/access/login');
};
</script>
