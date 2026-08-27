<template>
  <div class="settings-panel">
    <div class="status-strip q-mb-lg">
      <q-icon name="hub" size="21px" />
      <div class="col">
        <div class="text-weight-medium">地址、配额和访问凭据已集中到这里</div>
        <div class="text-caption">保存后下一次搜索立即使用新配置。运行健康、延迟和命中数据请在字幕源状态页查看。</div>
      </div>
      <q-btn flat dense no-caps label="运行状态" to="/suppliers" />
    </div>

    <section aria-labelledby="supplier-endpoints-title">
      <div class="row items-end q-mb-md">
        <div>
          <div id="supplier-endpoints-title" class="section-title">搜索源与入口地址</div>
          <div class="section-kicker">仅在默认地址失效或使用自建镜像时修改</div>
        </div>
      </div>
      <div class="supplier-config-grid">
        <article v-for="item in supplierList" :key="item.name" class="supplier-config-card">
          <div class="supplier-config-card__head">
            <div>
              <div class="supplier-config-card__name">{{ displayName(item.name) }}</div>
              <div class="text-caption text-grey-7">{{ sourceDescription(item.name) }}</div>
            </div>
            <q-badge v-if="!sourceEnabled(item)" outline color="grey">未启用</q-badge>
            <q-badge v-else-if="item.name === 'a4k'" outline color="warning">自建镜像</q-badge>
            <q-badge v-else outline color="primary">已启用</q-badge>
          </div>
          <div class="text-caption text-grey-7">入口地址</div>
          <div class="ellipsis text-mono q-mb-sm" :title="item.root_url">{{ item.root_url || '未设置' }}</div>
          <div class="row items-center">
            <div class="text-caption text-grey-7">
              日配额：{{ item.daily_download_limit < 0 ? '不限' : item.daily_download_limit }}
            </div>
            <q-space />
            <edit-sub-source-btn-dialog :data="item" @update="(data) => handleSubSourceUpdate(item, data)" />
          </div>
        </article>
      </div>
    </section>

    <q-separator class="q-my-xl" />

    <section aria-labelledby="supplier-credentials-title">
      <div id="supplier-credentials-title" class="section-title">源开关与访问凭据</div>
      <div class="section-kicker q-mb-md">公开源可直接启用；账户型字幕源需同时填写凭据</div>

      <q-list separator class="content-surface">
        <q-item v-if="credentials.animetosho_settings" tag="label" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">AnimeTosho</q-item-label>
            <q-item-label caption>仅用于动漫：按标题与播出/绝对集号严格匹配，下载独立简繁中文字幕附件。</q-item-label>
          </q-item-section>
          <q-item-section side top>
            <q-toggle v-model="credentials.animetosho_settings.enabled" aria-label="启用 AnimeTosho" />
          </q-item-section>
        </q-item>

        <q-item v-if="credentials.addic7ed_settings" tag="label" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">Addic7ed</q-item-label>
            <q-item-label caption>仅用于常规剧集：缓存节目 ID，并按季、集和发布版本匹配简繁中文字幕。</q-item-label>
          </q-item-section>
          <q-item-section side top>
            <q-toggle v-model="credentials.addic7ed_settings.enabled" aria-label="启用 Addic7ed" />
          </q-item-section>
        </q-item>

        <q-item tag="label" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">Assrt</q-item-label>
            <q-item-label caption>使用 Assrt API Token 搜索；配额和可用性以服务商账户为准。</q-item-label>
            <q-input
              v-if="credentials.assrt_settings.enabled"
              v-model="credentials.assrt_settings.token"
              type="password"
              autocomplete="new-password"
              label="API Token"
              outlined
              dense
              class="q-mt-md"
              :rules="[(value) => !credentials.assrt_settings.enabled || !!value || '启用时必须填写 Token']"
            />
          </q-item-section>
          <q-item-section side top
            ><q-toggle v-model="credentials.assrt_settings.enabled" aria-label="启用 Assrt"
          /></q-item-section>
        </q-item>

        <q-item v-if="credentials.subtitle_best_settings" tag="label" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">SubtitleBest</q-item-label>
            <q-item-label caption>基于 IMDb/TMDB 信息匹配，适合精确检索和浏览器内手动下载。</q-item-label>
            <q-input
              v-if="credentials.subtitle_best_settings.enabled"
              v-model="credentials.subtitle_best_settings.api_key"
              type="password"
              autocomplete="new-password"
              label="API Key"
              outlined
              dense
              class="q-mt-md"
              :rules="[(value) => !credentials.subtitle_best_settings.enabled || !!value || '启用时必须填写 API Key']"
            />
          </q-item-section>
          <q-item-section side top
            ><q-toggle v-model="credentials.subtitle_best_settings.enabled" aria-label="启用 SubtitleBest"
          /></q-item-section>
        </q-item>

        <q-item v-if="credentials.subdl_settings" tag="label" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">SubDL</q-item-label>
            <q-item-label caption>使用 IMDb/TMDB 精确匹配，支持单集字幕和完整季字幕包。</q-item-label>
            <q-input
              v-if="credentials.subdl_settings.enabled"
              v-model="credentials.subdl_settings.api_key"
              type="password"
              autocomplete="new-password"
              label="API Key"
              outlined
              dense
              class="q-mt-md"
              :rules="[(value) => !credentials.subdl_settings.enabled || !!value || '启用时必须填写 API Key']"
            />
          </q-item-section>
          <q-item-section side top
            ><q-toggle v-model="credentials.subdl_settings.enabled" aria-label="启用 SubDL"
          /></q-item-section>
        </q-item>

        <q-item v-if="credentials.open_subtitles_settings" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">OpenSubtitles.com</q-item-label>
            <q-item-label caption
              >通过 IMDb/TMDB 与 OpenSubtitles 文件散列精确匹配；下载需要 API Key 和账户登录。</q-item-label
            >
            <div v-if="credentials.open_subtitles_settings.enabled" class="credential-grid q-mt-md">
              <q-input
                v-model="credentials.open_subtitles_settings.api_key"
                type="password"
                autocomplete="new-password"
                label="API Key"
                outlined
                dense
                :rules="[(value) => !!value || '启用时必须填写 API Key']"
              />
              <q-input
                v-model="credentials.open_subtitles_settings.username"
                autocomplete="username"
                label="用户名"
                outlined
                dense
                :rules="[(value) => !!value || '启用时必须填写用户名']"
              />
              <q-input
                v-model="credentials.open_subtitles_settings.password"
                type="password"
                autocomplete="new-password"
                label="密码"
                outlined
                dense
                :rules="[(value) => !!value || '启用时必须填写密码']"
              />
            </div>
            <div v-if="credentials.open_subtitles_settings.enabled" class="row q-col-gutter-md q-mt-xs">
              <div class="col-12 col-md-4">
                <q-toggle v-model="credentials.open_subtitles_settings.use_hash" label="启用文件散列匹配" />
              </div>
              <div class="col-12 col-md-4">
                <q-toggle
                  v-model="credentials.open_subtitles_settings.include_ai_translated"
                  label="包含 AI 翻译字幕"
                />
              </div>
              <div class="col-12 col-md-4">
                <q-toggle
                  v-model="credentials.open_subtitles_settings.include_machine_translated"
                  label="包含机器翻译字幕"
                />
              </div>
            </div>
          </q-item-section>
          <q-item-section side top>
            <q-toggle v-model="credentials.open_subtitles_settings.enabled" aria-label="启用 OpenSubtitles.com" />
          </q-item-section>
        </q-item>

        <q-item v-if="credentials.subsource_settings" class="q-pa-md">
          <q-item-section>
            <q-item-label class="text-weight-medium">SubSource</q-item-label>
            <q-item-label caption>通过 IMDb 精确定位，支持中文字幕、单集结果和完整季字幕包。</q-item-label>
            <q-input
              v-if="credentials.subsource_settings.enabled"
              v-model="credentials.subsource_settings.api_key"
              type="password"
              autocomplete="new-password"
              label="API Key"
              outlined
              dense
              class="q-mt-md"
              :rules="[(value) => !!value || '启用时必须填写 API Key']"
            />
          </q-item-section>
          <q-item-section side top>
            <q-toggle v-model="credentials.subsource_settings.enabled" aria-label="启用 SubSource" />
          </q-item-section>
        </q-item>
      </q-list>
    </section>
  </div>
</template>

<script setup>
import { computed } from 'vue';
import { formModel } from 'pages/settings/use-settings';
import EditSubSourceBtnDialog from 'pages/settings/BtnDialogEditSubSource';

const credentials = computed(() => formModel.subtitle_sources || {});
const supplierList = computed(() => Object.values(formModel.advanced_settings?.suppliers_settings || {}));

const names = {
  xunlei: '迅雷字幕',
  shooter: '射手网',
  assrt: 'Assrt',
  a4k: 'A4K',
  subhd: 'SubHD',
  zimuku: '字幕库',
  subtitle_best: 'SubtitleBest',
  subdl: 'SubDL',
  animetosho: 'AnimeTosho',
  addic7ed: 'Addic7ed',
};

const descriptions = {
  a4k: '公共入口已退役，只保留自建兼容镜像配置',
  subtitle_best: 'API 聚合搜索与精确媒体识别',
  subdl: 'IMDb/TMDB 精确搜索与季包下载',
  assrt: 'Token 鉴权的字幕搜索接口',
  animetosho: '动漫独立字幕附件与绝对集号回退',
  addic7ed: '常规剧集简繁字幕与发布版本匹配',
};

const displayName = (name) => names[name] || name;
const sourceDescription = (name) => descriptions[name] || '自动搜索候选来源';
const sourceEnabled = (item) => {
  if (item.daily_download_limit === 0) return false;
  const toggles = {
    assrt: credentials.value.assrt_settings,
    subtitle_best: credentials.value.subtitle_best_settings,
    subdl: credentials.value.subdl_settings,
    animetosho: credentials.value.animetosho_settings,
    addic7ed: credentials.value.addic7ed_settings,
    open_subtitles: credentials.value.open_subtitles_settings,
    subsource: credentials.value.subsource_settings,
  };
  return toggles[item.name] ? !!toggles[item.name].enabled : true;
};
const handleSubSourceUpdate = (item, data) => {
  const target = formModel.advanced_settings.suppliers_settings[item.name];
  target.root_url = data.url;
  target.daily_download_limit = data.dailyLimit;
};
</script>

<style scoped>
.text-mono {
  font-family: ui-monospace, 'SFMono-Regular', Consolas, monospace;
  font-size: 12px;
}

.credential-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

@media (max-width: 1023px) {
  .credential-grid {
    grid-template-columns: 1fr;
  }
}
</style>
