<template>
  <div class="search-panel">
    <div class="status-strip q-mb-md"><q-icon name="open_in_new" />选择关键字后会在新标签页打开对应字幕站。</div>
    <div v-if="errorMessage" class="empty-state">
      <q-icon name="cloud_off" size="36px" class="empty-state__icon" />
      <div class="empty-state__title">无法生成搜索链接</div>
      <div>{{ errorMessage }}</div>
      <q-btn flat color="primary" label="重试" @click="getSearchInfo" />
    </div>
    <q-list v-else-if="searchInfo" separator class="content-surface">
      <q-item v-for="url in searchInfo?.search_url" :key="url">
        <q-item-section top side class="manual-search-domain text-bold text-black">
          {{ getDomain(url) }}
        </q-item-section>
        <q-item-section>
          <div class="row q-gutter-sm">
            <a
              v-for="item in searchInfo?.key_words"
              :key="item"
              :href="getSearchUrl(url, item)"
              target="_blank"
              rel="noopener noreferrer"
              style="text-decoration: none"
            >
              <q-badge class="cursor-pointer" color="secondary" title="点击跳转到网站搜索">{{ item }}</q-badge>
            </a>
          </div>
        </q-item-section>
      </q-item>
    </q-list>
    <q-inner-loading :showing="loading">
      <q-spinner size="50px" color="primary" />
    </q-inner-loading>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue';
import LibraryApi from 'src/api/LibraryApi';

const props = defineProps({
  path: String,
  isMovie: {
    type: Boolean,
    default: false,
  },
});

const searchInfo = ref(null);
const loading = ref(false);
const errorMessage = ref('');

const getSearchInfo = async () => {
  loading.value = true;
  errorMessage.value = '';
  const [data, err] = await LibraryApi.getSearchSubtitleInfo({
    video_f_path: props.path,
    is_movie: props.isMovie,
  });
  if (err !== null) {
    errorMessage.value = err.message || '服务端未返回搜索信息';
    loading.value = false;
    return;
  }
  searchInfo.value = data;
  loading.value = false;
};

const getDomain = (url) => {
  try {
    return new URL(url).hostname;
  } catch (_) {
    return '字幕站';
  }
};

const getSearchUrl = (url, keyword) => {
  if (url.includes('%s')) {
    return url.replace('%s', encodeURIComponent(keyword));
  }
  return url + encodeURIComponent(keyword);
};

onMounted(() => {
  getSearchInfo();
});
</script>
