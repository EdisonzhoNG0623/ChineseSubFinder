import { onMounted, reactive, ref, watch } from 'vue';
import CommonApi from 'src/api/CommonApi';

export const setupState = reactive({
  defaultSettings: null,
  form: {
    username: '',
    password: '',
    confirmPassword: '',
    movieFolder: [''],
    seriesFolder: [''],
    mediaServer: '',
    emby: {
      url: '',
      apiKey: '',
      limitCount: 3000,
      skipWatched: true,
      autoOrManual: true,
      movieFolderMap: {},
      seriesFolderMap: {},
    },
  },
});
export const setupLoading = ref(false);
export const setupError = ref('');

const getDefaultSettings = async () => {
  setupLoading.value = true;
  setupError.value = '';
  const [res, err] = await CommonApi.getDefaultSettings();
  setupLoading.value = false;
  if (err !== null) {
    setupError.value = err.message || '无法读取默认配置';
    return;
  }
  setupState.defaultSettings = res;
};
export const reloadSetup = () => getDefaultSettings();

const getFolderMap = (folders, maps) =>
  folders.reduce((r, a) => {
    if (Object.keys(maps).includes(a)) {
      r[a] = maps[a];
    } else {
      r[a] = '';
    }
    return r;
  }, {});

watch(
  () => setupState.form.movieFolder,
  () => {
    setupState.form.emby.movieFolderMap = getFolderMap(
      setupState.form.movieFolder,
      setupState.form.emby.movieFolderMap
    );
  },
  { deep: true }
);

watch(
  () => setupState.form.seriesFolder,
  () => {
    setupState.form.emby.seriesFolderMap = getFolderMap(
      setupState.form.seriesFolder,
      setupState.form.emby.seriesFolderMap
    );
  },
  { deep: true }
);

export const useSetup = () => {
  onMounted(() => {
    getDefaultSettings();
  });
};
