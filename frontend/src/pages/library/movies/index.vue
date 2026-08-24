<template>
  <q-page class="app-page">
    <section class="page-heading">
      <div>
        <div class="eyebrow">MEDIA LIBRARY</div>
        <h1>电影</h1>
        <p>浏览字幕覆盖情况、手动搜索或上传字幕，并按需加入下载队列。</p>
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
        aria-label="按字幕状态筛选"
      />
      <q-space />
      <q-input
        v-model="filterForm.search"
        outlined
        dense
        clearable
        debounce="180"
        placeholder="搜索电影名称"
        aria-label="搜索电影"
      >
        <template #prepend><q-icon name="search" /></template>
      </q-input>
    </div>

    <div v-if="libraryLoading" class="media-grid" aria-busy="true" aria-label="正在加载电影">
      <q-card v-for="n in 10" :key="n" flat class="media-card">
        <q-skeleton type="rect" style="aspect-ratio: 4 / 5" />
        <q-card-section><q-skeleton type="text" /><q-skeleton type="text" width="60%" /></q-card-section>
      </q-card>
    </div>

    <q-card v-else-if="libraryError" flat class="surface-card empty-state">
      <div>
        <q-icon name="cloud_off" size="40px" class="empty-state__icon" />
        <div class="empty-state__title">电影库加载失败</div>
        <div class="q-mb-md">{{ libraryError }}</div>
        <q-btn outline color="primary" label="重试" @click="reloadLibrary" />
      </div>
    </q-card>

    <div v-else-if="filteredMovies.length" class="media-grid">
      <q-intersection v-for="item in filteredMovies" :key="item.video_f_path" once class="media-intersection">
        <list-item-movie :data="item" />
      </q-intersection>
    </div>

    <q-card v-else flat class="surface-card empty-state">
      <div>
        <q-icon name="movie_filter" size="42px" class="empty-state__icon" />
        <div class="empty-state__title">{{ movies.length ? '没有符合筛选条件的电影' : '媒体库中还没有电影' }}</div>
        <div class="q-mb-md">
          {{ movies.length ? '调整搜索关键字或字幕状态。' : '刷新媒体缓存后，电影会显示在这里。' }}
        </div>
        <q-btn v-if="movies.length" flat color="primary" label="清除筛选" @click="clearFilters" />
      </div>
    </q-card>
  </q-page>
</template>

<script setup>
import { computed, reactive } from 'vue';
import { useLibrary } from 'pages/library/use-library';
import BtnDialogLibraryRefresh from 'pages/library/BtnLibraryRefresh';
import BtnDialogMediaServerSubtitleRefresh from 'pages/library/BtnMediaServerSubtitleRefresh';
import ListItemMovie from './ListItemMovie';

const filterForm = reactive({ hasSubtitle: 'all', search: '' });
const subtitleOptions = [
  { label: '全部', value: 'all' },
  { label: '已有字幕', value: 'with' },
  { label: '缺少字幕', value: 'without' },
];
const { movies, libraryLoading, libraryError, reloadLibrary } = useLibrary();

const filteredMovies = computed(() => {
  let result = movies.value;
  if (filterForm.hasSubtitle === 'with') result = result.filter((item) => item.sub_f_path_list.length > 0);
  if (filterForm.hasSubtitle === 'without') result = result.filter((item) => item.sub_f_path_list.length === 0);
  if (filterForm.search)
    result = result.filter((item) => item.name.toLowerCase().includes(filterForm.search.toLowerCase()));
  return result;
});

const clearFilters = () => {
  filterForm.hasSubtitle = 'all';
  filterForm.search = '';
};
</script>
