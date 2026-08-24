<template>
  <div class="settings-panel">
    <q-list dense class="settings-form-list">
      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>连接 Emby</q-item-label>
          <q-item-label caption>同步媒体信息、观看状态和字幕列表</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.enable" />
        </q-item-section>
      </q-item>

      <template v-if="form.enable">
        <q-item>
          <q-item-section>
            <q-item-label>Emby 服务地址</q-item-label>
            <q-item-label caption>填写当前容器可访问的完整 URL</q-item-label>
          </q-item-section>
          <q-item-section avatar>
            <q-input
              v-model="form.address_url"
              outlined
              label="例如 http://192.168.1.10:8096"
              dense
              :rules="[(val) => (form.enable && !!val) || '不能为空', validateServerUrl]"
            />
          </q-item-section>
        </q-item>
        <q-item>
          <q-item-section>
            <q-item-label>Emby API Key</q-item-label>
          </q-item-section>
          <q-item-section avatar>
            <q-input
              v-model="form.api_key"
              type="password"
              autocomplete="new-password"
              outlined
              dense
              :rules="[(val) => (form.enable && !!val) || '请输入 API Key']"
            />
          </q-item-section>
        </q-item>

        <q-item> <btn-check-emby-server /> </q-item>
        <q-item>
          <q-item-section>
            <q-item-label>单次同步上限</q-item-label>
            <q-item-label caption>限制每次从 Emby 获取的媒体数量</q-item-label>
          </q-item-section>
          <q-item-section avatar>
            <q-input
              v-model.number="form.max_request_video_number"
              outlined
              dense
              :rules="[(val) => (form.enable && !!val) || '不能为空', (val) => /^\d+$/.test(val) || '必须是整数']"
            />
          </q-item-section>
        </q-item>
        <q-item tag="label" v-ripple>
          <q-item-section>
            <q-item-label>跳过已观看内容</q-item-label>
          </q-item-section>
          <q-item-section avatar>
            <q-toggle v-model="form.skip_watched" />
          </q-item-section>
        </q-item>

        <q-separator spaced inset></q-separator>

        <q-item tag="label" v-ripple>
          <q-item-section>
            <q-item-label>自动匹配 IMDb ID</q-item-label>
            <q-item-label caption>推荐开启，用于提高精确字幕源的命中率</q-item-label>
          </q-item-section>
          <q-item-section avatar>
            <q-toggle v-model="form.auto_or_manual" />
          </q-item-section>
        </q-item>
      </template>
    </q-list>
  </div>
</template>

<script setup>
import { formModel } from 'pages/settings/use-settings';
import { toRefs } from '@vueuse/core';
import BtnCheckEmbyServer from 'pages/settings/BtnCheckEmbyServer';

const { emby_settings: form } = toRefs(formModel);

const validateServerUrl = (value) => {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) || '仅支持 HTTP(S) 地址';
  } catch (_) {
    return '请输入完整 URL';
  }
};
</script>
