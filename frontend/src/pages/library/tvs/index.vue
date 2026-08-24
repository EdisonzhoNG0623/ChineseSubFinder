<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">MEDIA LIBRARY</div>
        <h1>连续剧</h1>
        <p>按整季字幕覆盖率筛选剧集，批量锁定不再参与自动下载的内容。</p>
      </div>
      <div class="page-heading__actions row q-gutter-sm">
        <btn-dialog-library-refresh />
        <btn-dialog-media-server-subtitle-refresh />
      </div>
    </section>

    <div class="media-toolbar">
      <q-btn-toggle
        v-model="filterForm.hasSubtitle"
        unelevated
        toggle-color="primary"
        :options="subtitleOptions"
        aria-label="按字幕覆盖状态筛选"
      />
      <q-space />
      <q-input
        v-model="filterForm.search"
        outlined
        dense
        clearable
        debounce="180"
        placeholder="搜索连续剧名称"
        aria-label="搜索连续剧"
      >
        <template #prepend><q-icon name="search" /></template>
      </q-input>
    </div>

    <div v-if="selection.length" class="selection-bar">
      <div>
        <strong>已选择 {{ selection.length }} 部连续剧</strong><span>批量锁定后，其剧集将跳过自动字幕下载。</span>
      </div>
      <div class="row q-gutter-sm">
        <q-btn flat color="primary" label="清除选择" @click="selection = []" />
        <q-btn outline color="primary" icon="lock_open" label="取消锁定" @click="setLock(false)" />
        <q-btn unelevated color="primary" icon="lock" label="锁定" @click="setLock(true)" />
      </div>
    </div>

    <div v-if="libraryLoading" class="media-grid" aria-busy="true" aria-label="正在加载连续剧">
      <q-card v-for="n in 10" :key="n" flat class="media-card">
        <q-skeleton type="rect" style="aspect-ratio: 4 / 5" />
        <q-card-section><q-skeleton type="text" /><q-skeleton type="text" width="60%" /></q-card-section>
      </q-card>
    </div>

    <q-card v-else-if="libraryError" flat class="surface-card empty-state">
      <div>
        <q-icon name="cloud_off" size="40px" class="empty-state__icon" />
        <div class="empty-state__title">连续剧库加载失败</div>
        <div class="q-mb-md">{{ libraryError }}</div>
        <q-btn outline color="primary" label="重试" @click="reloadLibrary" />
      </div>
    </q-card>

    <div v-else-if="filteredTvs.length" class="media-grid">
      <q-intersection v-for="item in filteredTvs" :key="item.root_dir_path" once class="media-intersection">
        <div
          class="media-selectable cursor-pointer"
          :class="{ 'media-selectable--selected': selection.includes(item.root_dir_path) }"
          @click="toggleSelection(item)"
        >
          <list-item-t-v :data="item" />
          <q-checkbox
            :model-value="selection.includes(item.root_dir_path)"
            class="media-selectable__checkbox"
            :aria-label="`选择 ${item.name}`"
            @click.stop
            @update:model-value="toggleSelection(item)"
          />
        </div>
      </q-intersection>
    </div>

    <q-card v-else flat class="surface-card empty-state">
      <div>
        <q-icon name="video_library" size="42px" class="empty-state__icon" />
        <div class="empty-state__title">{{ tvs.length ? '没有符合筛选条件的连续剧' : '媒体库中还没有连续剧' }}</div>
        <div class="q-mb-md">
          {{ tvs.length ? '调整搜索关键字或字幕覆盖状态。' : '刷新媒体缓存后，连续剧会显示在这里。' }}
        </div>
        <q-btn v-if="tvs.length" flat color="primary" label="清除筛选" @click="clearFilters" />
      </div>
    </q-card>
  </q-page>
</template>

<script setup>
import { computed, reactive, ref } from 'vue';
import { useQuasar } from 'quasar';
import { useLibrary } from 'pages/library/use-library';
import ListItemTV from 'pages/library/tvs/ListItemTV';
import BtnDialogLibraryRefresh from 'pages/library/BtnLibraryRefresh';
import BtnDialogMediaServerSubtitleRefresh from 'pages/library/BtnMediaServerSubtitleRefresh';
import { SystemMessage } from 'src/utils/message';
import LibraryApi from 'src/api/LibraryApi';
import { VIDEO_TYPE_TV } from 'src/constants/SettingConstants';

const $q = useQuasar();
const filterForm = reactive({ hasSubtitle: 'all', search: '' });
const subtitleOptions = [
  { label: '全部', value: 'all' },
  { label: '无字幕', value: 'none' },
  { label: '部分覆盖', value: 'partial' },
  { label: '全部覆盖', value: 'complete' },
];
const selection = ref([]);
const { tvs, libraryLoading, libraryError, reloadLibrary } = useLibrary();

const getSubtitleCount = (item) =>
  (item.one_video_info || []).filter((episode) => episode.sub_f_path_list?.length > 0).length;
const filteredTvs = computed(() => {
  let result = tvs.value;
  if (filterForm.hasSubtitle === 'none') result = result.filter((item) => getSubtitleCount(item) === 0);
  if (filterForm.hasSubtitle === 'partial')
    result = result.filter((item) => {
      const count = getSubtitleCount(item);
      return count > 0 && count < (item.one_video_info || []).length;
    });
  if (filterForm.hasSubtitle === 'complete')
    result = result.filter(
      (item) => (item.one_video_info || []).length > 0 && getSubtitleCount(item) === item.one_video_info.length
    );
  if (filterForm.search)
    result = result.filter((item) => item.name.toLowerCase().includes(filterForm.search.toLowerCase()));
  return result;
});
const clearFilters = () => {
  filterForm.hasSubtitle = 'all';
  filterForm.search = '';
};
const toggleSelection = (item) => {
  const key = item.root_dir_path;
  selection.value = selection.value.includes(key)
    ? selection.value.filter((itemKey) => itemKey !== key)
    : [...selection.value, key];
};
const lockTv = async (item, lock) => {
  const [tvInfo] = await LibraryApi.getTvDetail({
    name: item.name,
    main_root_dir_f_path: item.main_root_dir_f_path,
    root_dir_path: item.root_dir_path,
  });
  return LibraryApi.setSkipInfo({
    video_skip_infos: tvInfo.one_video_info.map((episode) => ({
      video_type: VIDEO_TYPE_TV,
      physical_video_file_full_path: episode.video_f_path,
      is_bluray: false,
      is_skip: lock,
    })),
  });
};
const setLock = (flag) => {
  $q.dialog({
    title: flag ? '锁定所选连续剧' : '取消锁定所选连续剧',
    message: flag
      ? `确认锁定 ${selection.value.length} 部连续剧？锁定后将跳过自动字幕下载。`
      : `确认取消 ${selection.value.length} 部连续剧的锁定？`,
    cancel: { label: '取消', flat: true },
    ok: { label: '确认', unelevated: true, color: 'primary' },
  }).onOk(async () => {
    const selectedItems = selection.value
      .map((key) => tvs.value.find((item) => item.root_dir_path === key))
      .filter(Boolean);
    const results = await Promise.allSettled(selectedItems.map((item) => lockTv(item, flag)));
    const failed = results.filter((result) => result.status === 'rejected').length;
    selection.value = [];
    if (failed) SystemMessage.error(`${results.length - failed} 项成功，${failed} 项失败`);
    else SystemMessage.success('批量操作已完成');
  });
};
</script>
