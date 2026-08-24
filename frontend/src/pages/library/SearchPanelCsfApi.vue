<template>
  <div class="search-panel">
    <subtitle-best-api-limit-banner v-if="!hideLimit" />
    <q-list v-if="csfSearchResult?.length" separator>
      <q-item v-for="(item, index) in csfSearchResult" :key="item.sub_sha256">
        <q-item-section>
          <div class="row items-center q-gutter-sm">
            <div>{{ index + 1 }}. {{ item.title }}</div>
            <q-badge color="primary">{{ LANGUAGES[item.language] }}</q-badge>
            <q-badge
              v-if="cacheBlob[item.sub_sha256]"
              color="grey"
              title="已缓存到浏览器，再次预览和下载不消耗次数，关闭窗口后失效"
              >已缓存</q-badge
            >
          </div>
        </q-item-section>
        <q-item-section side>
          <div class="row">
            <btn-dialog-preview-video
              :path="path"
              :subtitle-url-list="[selectedSubUrl]"
              :on-btn-click="(callback) => handlePreviewClick(item, callback)"
              :subtitle-type="selectedItem?.ext.replace('.', '')"
            />
            <q-btn
              v-if="!hideDownload"
              color="primary"
              icon="download"
              flat
              dense
              @click="handleDownloadCsfSub(item)"
              title="下载"
            />
          </div>
        </q-item-section>
      </q-item>
    </q-list>
    <div v-else-if="!loading" class="text-grey">
      <div>
        <span>IMDb ID：{{ imdbId || '未识别' }}</span>
        <span v-if="imdbId && !isImdbId(imdbId)" class="q-ml-md text-negative">IMDb ID 格式无效</span>
      </div>
      <template v-if="tmdbErrorMsg">
        <div class="text-negative">{{ tmdbErrorMsg }}</div>
        <div><q-btn flat label="重试" color="primary" dense @click="searchCsf" /></div>
      </template>
      <template v-else-if="subtitleBestApiErrorMsg">
        <div class="text-negative">字幕列表获取失败：{{ subtitleBestApiErrorMsg }}</div>
        <div><q-btn flat label="重试" color="primary" dense @click="searchCsf" /></div>
      </template>
      <template v-else>
        <div>没有找到匹配字幕。<q-btn flat label="重试" color="primary" dense @click="searchCsf" /></div>
        <div>
          若提示缺少 API Key，请前往<router-link class="text-primary" to="/settings?tab=subSource"
            >字幕源与凭据</router-link
          >配置 SubtitleBest。
        </div>
      </template>
    </div>
    <q-inner-loading :showing="loading">
      <q-spinner size="50px" color="primary" />
      <div>{{ loadingMsg }}</div>
      <div v-if="countdownLoading">预计 {{ nextRequestCountdownSecond }} 秒后取得数据</div>
    </q-inner-loading>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue';
import LibraryApi from 'src/api/LibraryApi';
import { SystemMessage } from 'src/utils/message';
import CsfSubtitlesApi from 'src/api/CsfSubtitlesApi';
import BtnDialogPreviewVideo from 'pages/library/BtnDialogPreviewVideo.vue';
import { getSubtitleUploadList } from 'pages/library/use-library';
import eventBus from 'vue3-eventbus';
import { useQuasar } from 'quasar';
import { LANGUAGES } from 'src/constants/LibraryConstants';
import { VIDEO_TYPE_MOVIE, VIDEO_TYPE_TV } from 'src/constants/SettingConstants';
import { settingsState } from 'src/store/settingsState';
import SubtitleBestApiLimitBanner from 'components/SubtitleBestApiLimitBanner.vue';
import { useApiLimit } from 'src/composables/use-api-limit';
import { isImdbId } from 'src/utils/common';

const props = defineProps({
  path: String,
  isMovie: {
    type: Boolean,
    default: false,
  },
  season: {
    type: Number,
  },
  episode: {
    type: Number,
  },
  // 隐藏下载按钮
  hideDownload: {
    type: Boolean,
    default: false,
  },
  // 隐藏额度
  hideLimit: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits(['getResult']);

const $q = useQuasar();
const { nextRequestCountdownSecond, countdownLoading, waitRequestReady } = useApiLimit(
  'lastSubtitleBestRequestTime',
  5 * 1000
);

const tmdbErrorMsg = ref('');
const subtitleBestApiErrorMsg = ref('');
const loading = ref(false);
const loadingMsg = ref('');
const csfSearchResult = ref(null);
const selectedSubBlob = ref(null);
const selectedItem = ref(null);
const imdbId = ref(null);

// blob缓存
const cacheBlob = ref({});

const selectedSubUrl = computed(() => {
  if (selectedSubBlob.value) {
    return URL.createObjectURL(selectedSubBlob.value);
  }
  return null;
});

const setLock = async () => {
  const [, err] = await LibraryApi.setSkipInfo({
    video_skip_infos: [
      {
        video_type: props.isMovie ? VIDEO_TYPE_MOVIE : VIDEO_TYPE_TV,
        physical_video_file_full_path: props.path,
        is_bluray: false,
        is_skip: true,
      },
    ],
  });
  if (err !== null) {
    SystemMessage.error(err.message);
  } else {
    // 通知列表页锁定成功
    eventBus.emit(`refresh-skip-status-${props.path}`, true);
    SystemMessage.success('操作成功');
  }
};

const searchCsf = async () => {
  loading.value = true;
  subtitleBestApiErrorMsg.value = '';
  loadingMsg.value = '正在从 TMDB 获取媒体信息…';
  const [d, e] = await LibraryApi.getImdbId({
    is_movie: props.isMovie,
    video_f_path: props.path,
  });
  if (e) {
    if (settingsState.settings.advanced_settings.tmdb_api_settings.enable) {
      tmdbErrorMsg.value = 'TMDB 查询失败。当前使用自定义 API Key，请检查“匹配与队列”中的 TMDB 配置。';
    } else {
      tmdbErrorMsg.value = 'TMDB 公共查询暂时不可用，可稍后重试或在“匹配与队列”中配置自己的 API Key。';
    }
    loading.value = false;
    loadingMsg.value = '';
    SystemMessage.error(e.message);
    return;
  }
  tmdbErrorMsg.value = '';
  loadingMsg.value = '正在从 SubtitleBest 获取字幕列表…';
  imdbId.value = d?.ImdbId;

  if (!isImdbId(imdbId.value)) {
    loadingMsg.value = '';
    loading.value = false;
    return;
  }

  if (props.isMovie) {
    const [data, err] = await CsfSubtitlesApi.searchMovie({
      imdb_id: imdbId.value,
    });
    if (err !== null) {
      subtitleBestApiErrorMsg.value = err.message;
      SystemMessage.error(err.message);
    } else {
      csfSearchResult.value = data.subtitles;
    }
  } else {
    const [data, err] = await CsfSubtitlesApi.searchTvEps({
      imdb_id: imdbId.value,
      season: props.season,
      episode: props.episode,
    });
    if (err !== null) {
      subtitleBestApiErrorMsg.value = err.message;
      SystemMessage.error(err.message);
    } else {
      csfSearchResult.value = data.subtitles;
    }
  }
  emit('getResult', csfSearchResult.value);
  loadingMsg.value = '';
  loading.value = false;
};

const fetchSubtitleBlob = async (item) => {
  selectedItem.value = item;
  if (cacheBlob.value[item.sub_sha256]) {
    selectedSubBlob.value = cacheBlob.value[item.sub_sha256];
    return;
  }
  selectedSubBlob.value = null;
  loading.value = true;
  loadingMsg.value = '正在获取下载地址…';
  await waitRequestReady();

  loadingMsg.value = '正在下载字幕…';
  const [data, err] = await CsfSubtitlesApi.getDownloadUrl({
    ...item,
    imdb_id: imdbId.value,
  });
  if (err !== null) {
    SystemMessage.error(err.message);
  } else {
    // fetch资源，获取blob url
    const res = await fetch(data.download_link);
    const blob = await res.blob();
    cacheBlob.value = {
      ...cacheBlob.value,
      [item.sub_sha256]: blob,
    };
    selectedSubBlob.value = blob;
  }
  loadingMsg.value = '';
  loading.value = false;
};

const handleDownloadCsfSub = async (item) => {
  await fetchSubtitleBlob(item);

  if (!selectedSubBlob.value) {
    return;
  }

  // 上传
  const formData = new FormData();
  formData.append('video_f_path', props.path);
  formData.append('file', new File([selectedSubBlob.value], item.title, { type: 'text/plain' }));
  await LibraryApi.uploadSubtitle(formData);
  await getSubtitleUploadList();
  eventBus.emit('subtitle-uploaded');

  $q.dialog({
    title: '操作确认',
    message: `已下载到库中，是否锁定该视频，无需再次自动下载字幕？`,
    cancel: true,
    persistent: true,
    focus: 'none',
  }).onOk(async () => {
    setLock();
  });

  SystemMessage.success('已下载到库中');
};

const handlePreviewClick = async (item, callback) => {
  await fetchSubtitleBlob(item);
  if (selectedSubUrl.value) {
    callback(true);
  } else {
    callback(false);
  }
};

onMounted(() => {
  searchCsf();
});
</script>
