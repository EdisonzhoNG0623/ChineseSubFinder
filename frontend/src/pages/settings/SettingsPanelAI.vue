<template>
  <div class="settings-panel">
    <q-banner rounded class="bg-indigo-1 text-indigo-10 q-mb-lg">
      AI 不是主搜索引擎。只有确定性集号规则和候选评分无法区分结果时才调用，并且允许模型弃权，避免错误字幕覆盖正确匹配。
    </q-banner>
    <q-list dense style="max-width: 680px">
      <q-item tag="label">
        <q-item-section
          ><q-item-label>启用 AI 歧义识别</q-item-label
          ><q-item-label caption>兼容 OpenAI Chat Completions API；默认关闭。</q-item-label></q-item-section
        >
        <q-item-section side><q-toggle v-model="form.enabled" /></q-item-section>
      </q-item>
      <q-separator spaced />
      <q-item
        ><q-item-section
          ><q-input
            v-model.trim="form.base_url"
            label="API Base URL"
            placeholder="https://api.openai.com/v1"
            outlined
            dense
            :disable="!form.enabled"
            :rules="[validateURL]"
            hint="仅允许 HTTP(S)，默认要求 HTTPS" /></q-item-section
      ></q-item>
      <q-item
        ><q-item-section
          ><q-input
            v-model="form.api_key"
            type="password"
            label="API Key"
            autocomplete="new-password"
            outlined
            dense
            :disable="!form.enabled"
            hint="界面只返回掩码，保存时不会覆盖已有密钥" /></q-item-section
      ></q-item>
      <q-item
        ><q-item-section
          ><q-input
            v-model.trim="form.model"
            label="模型名称"
            placeholder="gpt-4.1-mini"
            outlined
            dense
            :disable="!form.enabled"
            :rules="[(value) => !form.enabled || !!value || '启用时不能为空']" /></q-item-section
      ></q-item>
      <div class="row q-col-gutter-md">
        <div class="col-12 col-sm-6">
          <q-item
            ><q-item-section
              ><q-input
                v-model.number="form.min_confidence"
                type="number"
                min="0.5"
                max="1"
                step="0.01"
                label="最低置信度"
                outlined
                dense
                :disable="!form.enabled"
                :rules="[(value) => (value >= 0.5 && value <= 1) || '范围为 0.5–1']" /></q-item-section
          ></q-item>
        </div>
        <div class="col-12 col-sm-6">
          <q-item
            ><q-item-section
              ><q-input
                v-model.number="form.timeout_seconds"
                type="number"
                min="3"
                max="60"
                suffix="秒"
                label="请求超时"
                outlined
                dense
                :disable="!form.enabled"
                :rules="[(value) => (value >= 3 && value <= 60) || '范围为 3–60 秒']" /></q-item-section
          ></q-item>
        </div>
      </div>
      <q-item tag="label"
        ><q-item-section
          ><q-item-label>允许不安全的 HTTP</q-item-label
          ><q-item-label caption>仅用于可信局域网内的自托管兼容接口；公网地址请保持关闭。</q-item-label></q-item-section
        ><q-item-section side><q-toggle v-model="form.allow_insecure_http" :disable="!form.enabled" /></q-item-section
      ></q-item>
      <q-separator spaced />
      <q-item
        ><q-item-section
          ><q-item-label>连接测试</q-item-label
          ><q-item-label caption>先保存本页配置，再发出不包含真实媒体信息的合成候选测试。</q-item-label></q-item-section
        ><q-item-section side
          ><q-btn
            outline
            color="primary"
            icon="bolt"
            label="测试"
            :disable="!form.enabled"
            :loading="testing"
            @click="testConnection" /></q-item-section
      ></q-item>
    </q-list>
  </div>
</template>

<script setup>
import { ref, toRef } from 'vue';
import AIApi from 'src/api/AIApi';
import { formModel } from 'pages/settings/use-settings';
import { SystemMessage } from 'src/utils/message';

const form = toRef(formModel.experimental_function, 'ai_settings');
const testing = ref(false);
const validateURL = (value) => {
  if (!form.value.enabled) return true;
  try {
    const url = new URL(value);
    if (!['https:', 'http:'].includes(url.protocol)) return '仅支持 HTTP(S)';
    if (url.protocol === 'http:' && !form.value.allow_insecure_http) return 'HTTP 需要明确开启“不安全 HTTP”';
    return true;
  } catch (_) {
    return '请输入完整 URL';
  }
};
const testConnection = async () => {
  testing.value = true;
  const [res, err] = await AIApi.test();
  testing.value = false;
  if (err) {
    SystemMessage.error(err.error?.data?.message || err.message);
    return;
  }
  SystemMessage.success(`连接成功，模型决策：${res.decision || 'ABSTAIN'}`);
};
</script>
