import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import LibraryApi from 'src/api/LibraryApi';
import { SystemMessage } from 'src/utils/message';
import config from 'src/config';
import { LocalStorage } from 'quasar';
import { useSettings } from 'pages/settings/use-settings';
import { userState } from 'src/store/userState';

export const getBackendUrl = (basePath) => {
  const backendUrl = config.BACKEND_URL.replace(/\/+$/, '');
  const normalizedPath = basePath.split(/\/|\\/).join('/');
  const separator = normalizedPath.startsWith('/') ? '' : '/';
  return `${backendUrl}${separator}${normalizedPath}`;
};

export const isCrossOriginResourceUrl = (resourceUrl, currentLocation = window.location.href) => {
  try {
    const currentUrl = new URL(currentLocation);
    return new URL(resourceUrl, currentUrl).origin !== currentUrl.origin;
  } catch {
    // Fail closed without copying a read-only capability into a malformed or
    // unintended destination.
    return false;
  }
};

export const getUrl = (basePath) => {
  const resourceUrl = getBackendUrl(basePath);
  // Same-origin browser-native requests use the HttpOnly resource cookie.
  // Only an independent cross-origin BACKEND_URL needs a query capability.
  if (!userState.resourceTicket || !isCrossOriginResourceUrl(resourceUrl)) return resourceUrl;

  const hashIndex = resourceUrl.indexOf('#');
  const resourceWithoutHash = hashIndex === -1 ? resourceUrl : resourceUrl.slice(0, hashIndex);
  const hash = hashIndex === -1 ? '' : resourceUrl.slice(hashIndex);
  const separator = resourceWithoutHash.includes('?') ? '&' : '?';
  return `${resourceWithoutHash}${separator}resource_ticket=${encodeURIComponent(userState.resourceTicket)}${hash}`;
};

// 封面规则
export const coverRule = ref(LocalStorage.getItem('coverRule') ?? 'poster.jpg');

export const originMovies = ref([]);
export const originTvs = ref([]);
const movies = computed(() =>
  originMovies.value.map((movie) => ({
    ...movie,
  }))
);

const tvs = computed(() =>
  originTvs.value.map((tv) => ({
    ...tv,
  }))
);
export const libraryRefreshStatus = ref(null);
export const subtitleUploadList = ref([]);
export const libraryLoading = ref(false);
export const libraryError = ref('');

export const refreshCacheLoading = computed(() => libraryRefreshStatus.value === 'running');

let getRefreshStatusTimer = null;
let waitForRefreshTimer = null;
let resolveRefreshWait = null;

export const getLibraryRefreshStatus = async () => {
  const [res, err] = await LibraryApi.getRefreshStatus();
  if (err || !res) return false;
  libraryRefreshStatus.value = res.status;
  return true;
};

export const getLibraryList = async () => {
  libraryLoading.value = true;
  libraryError.value = '';
  const [res, err] = await LibraryApi.getList();
  libraryLoading.value = false;
  if (err !== null) {
    libraryError.value = err.message || '媒体库加载失败';
  } else {
    originMovies.value = res.movie_infos_v2;
    originTvs.value = res.season_infos_v2;
  }
};

export const checkLibraryRefreshStatus = async () => {
  libraryRefreshStatus.value = null;
  const available = await getLibraryRefreshStatus();
  if (!available) return false;
  if (libraryRefreshStatus.value !== 'running') {
    await getLibraryList();
    return true;
  }
  getRefreshStatusTimer = setInterval(() => {
    getLibraryRefreshStatus();
  }, 1000);
  return new Promise((resolve) => {
    resolveRefreshWait = resolve;
    waitForRefreshTimer = setInterval(async () => {
      if (libraryRefreshStatus.value !== 'stopped') return;
      clearInterval(waitForRefreshTimer);
      waitForRefreshTimer = null;
      clearInterval(getRefreshStatusTimer);
      getRefreshStatusTimer = null;
      await getLibraryList();
      resolveRefreshWait?.(true);
      resolveRefreshWait = null;
    }, 250);
  });
};

export const refreshLibrary = async () => {
  const [, err] = await LibraryApi.refreshLibrary();
  if (err !== null) {
    SystemMessage.error(err.message);
  } else {
    const completed = await checkLibraryRefreshStatus();
    if (completed) SystemMessage.success('媒体缓存刷新已完成');
  }
};

export const getSubtitleUploadList = async () => {
  const [res, err] = await LibraryApi.getSubTitleQueueList();
  if (err || !res) return;
  subtitleUploadList.value = res.jobs;
};

export const useLibrary = () => {
  useSettings();

  const getSubtitleUploadListTimer = setInterval(() => {
    getSubtitleUploadList();
  }, 5000);

  onMounted(() => {
    getLibraryList();
    getSubtitleUploadList();
    checkLibraryRefreshStatus();
  });

  onBeforeUnmount(() => {
    clearInterval(getRefreshStatusTimer);
    clearInterval(waitForRefreshTimer);
    clearInterval(getSubtitleUploadListTimer);
    resolveRefreshWait?.(false);
    resolveRefreshWait = null;
  });

  return {
    movies,
    tvs,
    refreshLibrary,
    refreshCacheLoading,
    libraryLoading,
    libraryError,
    reloadLibrary: getLibraryList,
  };
};

export const doFixSubtitleTimeline = async (path) => {
  const formData = new FormData();
  formData.append('video_f_path', path);
  const subtitleUrl = getUrl(path);
  // 先下载字幕到内存，生成file文件
  const res = await fetch(subtitleUrl);
  if (!res.ok) {
    SystemMessage.error('获取字幕文件失败');
    return;
  }
  const blob = await res.blob();
  const file = new File([blob], path.split(/\/|\\/).pop());
  formData.append('file', file);
  await LibraryApi.uploadSubtitle(formData);
  SystemMessage.success('已提交时间轴校准', {
    timeout: 3000,
  });
  await getSubtitleUploadList();
};

/**
 * 检查一个视频是否锁定
 * @param videoInfo {video_type, physical_video_file_full_path, is_bluray, is_skip}
 * @returns {Promise<boolean>}
 */
export const checkIsVideoLocked = async (videoInfo) => {
  const [res] = await LibraryApi.getSkipInfo({
    video_skip_infos: [videoInfo],
  });
  return !!res.is_skips?.[0];
};
