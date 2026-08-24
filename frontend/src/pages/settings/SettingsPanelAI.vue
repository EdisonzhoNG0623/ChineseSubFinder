<template>
  <div class="settings-panel">
    <div class="status-strip q-mb-lg">
      <q-icon name="psychology_alt" size="22px" />
      <div>
        <div class="text-weight-medium">AI 是受约束的回退判断器，不是字幕搜索引擎</div>
        <div class="text-caption">仅当集号映射和确定性评分仍无法区分候选时调用；模型只能选择现有候选或弃权。</div>
      </div>
    </div>

    <q-card flat class="content-surface q-mb-lg">
      <q-card-section class="row items-center q-col-gutter-md">
        <div class="col">
          <div class="section-title">运行状态</div>
          <div class="text-caption text-grey-7">{{ statusText }}</div>
        </div>
        <q-badge outline :color="statusColor">{{ statusLabel }}</q-badge>
        <q-btn flat round dense icon="refresh" aria-label="刷新 AI 状态" :loading="statusLoading" @click="loadStatus" />
      </q-card-section>
      <q-separator />
      <q-card-section class="row q-col-gutter-lg">
        <div class="col-6 col-sm-3">
          <div class="metric-label">调用</div>
          <div class="text-h6">{{ status.runtime?.attempts || 0 }}</div>
        </div>
        <div class="col-6 col-sm-3">
          <div class="metric-label">匹配</div>
          <div class="text-h6 text-positive">{{ status.runtime?.matches || 0 }}</div>
        </div>
        <div class="col-6 col-sm-3">
          <div class="metric-label">弃权</div>
          <div class="text-h6">{{ status.runtime?.abstentions || 0 }}</div>
        </div>
        <div class="col-6 col-sm-3">
          <div class="metric-label">错误</div>
          <div class="text-h6 text-negative">{{ status.runtime?.errors || 0 }}</div>
        </div>
      </q-card-section>
    </q-card>

    <q-list>
      <q-item tag="label">
        <q-item-section>
          <q-item-label class="text-weight-medium">启用 AI 歧义识别</q-item-label>
          <q-item-label caption>默认关闭；只有启用且配置完整时才会发出请求。</q-item-label>
        </q-item-section>
        <q-item-section side><q-toggle v-model="form.enabled" aria-label="启用 AI 歧义识别" /></q-item-section>
      </q-item>

      <q-separator spaced />

      <q-item>
        <q-item-section>
          <q-select
            v-model="providerPreset"
            :options="providerOptions"
            label="快速配置服务商"
            outlined
            dense
            clearable
            emit-value
            map-options
            :disable="!form.enabled"
            hint="选择预设只填写地址和推荐模型，不会修改 API Key"
            @update:model-value="applyPreset"
          />
        </q-item-section>
      </q-item>

      <q-item>
        <q-item-section>
          <q-input
            v-model.trim="form.base_url"
            label="API Base URL"
            placeholder="https://api.example.com/v1"
            outlined
            dense
            :disable="!form.enabled"
            :rules="[validateURL]"
            hint="兼容 OpenAI Chat Completions；不要在这里追加 /chat/completions"
          />
        </q-item-section>
      </q-item>

      <q-item>
        <q-item-section>
          <q-input
            v-model="form.api_key"
            type="password"
            label="API Key"
            autocomplete="new-password"
            outlined
            dense
            :disable="!form.enabled"
            hint="读取时仅显示掩码；留在掩码状态保存不会覆盖已有密钥"
          />
        </q-item-section>
      </q-item>

      <q-item>
        <q-item-section>
          <q-input
            v-model.trim="form.model"
            label="模型 ID"
            placeholder="deepseek-v4-flash"
            outlined
            dense
            :disable="!form.enabled"
            :rules="[(value) => !form.enabled || !!value || '启用时必须填写模型 ID']"
          />
        </q-item-section>
      </q-item>

      <div class="row q-col-gutter-md">
        <div class="col-12 col-sm-6">
          <q-item
            ><q-item-section>
              <q-input
                v-model.number="form.min_confidence"
                type="number"
                min="0.5"
                max="1"
                step="0.01"
                label="最低置信度"
                outlined
                dense
                :disable="!form.enabled"
                :rules="[(value) => (value >= 0.5 && value <= 1) || '范围为 0.5–1']"
              /> </q-item-section
          ></q-item>
        </div>
        <div class="col-12 col-sm-6">
          <q-item
            ><q-item-section>
              <q-input
                v-model.number="form.timeout_seconds"
                type="number"
                min="3"
                max="60"
                suffix="秒"
                label="请求超时"
                outlined
                dense
                :disable="!form.enabled"
                :rules="[(value) => (value >= 3 && value <= 60) || '范围为 3–60 秒']"
              /> </q-item-section
          ></q-item>
        </div>
      </div>

      <q-item tag="label">
        <q-item-section>
          <q-item-label>允许不安全的 HTTP</q-item-label>
          <q-item-label caption>只用于可信局域网中的自托管服务；公网服务应始终使用 HTTPS。</q-item-label>
        </q-item-section>
        <q-item-section side><q-toggle v-model="form.allow_insecure_http" :disable="!form.enabled" /></q-item-section>
      </q-item>

      <q-separator spaced />

      <q-item>
        <q-item-section>
          <q-item-label class="text-weight-medium">连接与响应格式测试</q-item-label>
          <q-item-label caption>先保存设置，再发送不包含真实媒体信息的合成候选测试。</q-item-label>
          <div
            v-if="testResult"
            class="q-mt-sm text-caption"
            :class="testResult.ok ? 'text-positive' : 'text-negative'"
            role="status"
          >
            {{ testResult.message }}
          </div>
        </q-item-section>
        <q-item-section side>
          <q-btn
            outline
            color="primary"
            icon="bolt"
            label="测试连接"
            :disable="!form.enabled"
            :loading="testing"
            @click="testConnection"
          />
        </q-item-section>
      </q-item>
    </q-list>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref, toRef } from 'vue';
import AIApi from 'src/api/AIApi';
import { formModel } from 'pages/settings/use-settings';

const form = toRef(formModel.experimental_function, 'ai_settings');
const testing = ref(false);
const statusLoading = ref(false);
const testResult = ref(null);
const providerPreset = ref(null);
const status = reactive({ runtime: {} });
const providerOptions = [
  {
    label: 'OpenCode Go · DeepSeek V4 Flash',
    value: 'opencode-go',
    baseURL: 'https://opencode.ai/zen/go/v1',
    model: 'deepseek-v4-flash',
  },
  {
    label: 'OpenCode Zen · DeepSeek V4 Flash',
    value: 'opencode-zen',
    baseURL: 'https://opencode.ai/zen/v1',
    model: 'deepseek-v4-flash',
  },
  { label: 'OpenAI 官方兼容接口', value: 'openai', baseURL: 'https://api.openai.com/v1', model: 'gpt-4.1-mini' },
];

const statusLabel = computed(() => {
  if (!status.enabled) return '未启用';
  if (!status.configured) return '配置不完整';
  if (status.runtime?.errors > 0 && !status.runtime?.matches) return '需要检查';
  return '已就绪';
});
const statusColor = computed(
  () => ({ 未启用: 'grey', 配置不完整: 'warning', 需要检查: 'negative', 已就绪: 'positive' }[statusLabel.value])
);
const statusText = computed(() => {
  if (!status.enabled) return '开启并保存配置后才会在候选歧义时调用。';
  if (!status.configured) return 'Base URL 和模型 ID 尚未配置完整。';
  return `${status.model || form.value.model || '已配置模型'} · 最低置信度 ${
    status.min_confidence || form.value.min_confidence
  }`;
});

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

const applyPreset = (value) => {
  const preset = providerOptions.find((item) => item.value === value);
  if (!preset) return;
  form.value.base_url = preset.baseURL;
  form.value.model = preset.model;
};

const loadStatus = async () => {
  statusLoading.value = true;
  const [res, err] = await AIApi.getStatus();
  statusLoading.value = false;
  if (!err) Object.assign(status, res);
};

const testConnection = async () => {
  testing.value = true;
  testResult.value = null;
  const [res, err] = await AIApi.test();
  testing.value = false;
  if (err) {
    testResult.value = { ok: false, message: err.error?.data?.message || err.message || '连接测试失败' };
    return;
  }
  testResult.value = { ok: true, message: `连接成功，模型决策：${res.decision || 'ABSTAIN'}` };
  loadStatus();
};

onMounted(loadStatus);
</script>
