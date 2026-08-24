<template>
  <q-card flat class="media-card">
    <div class="media-card__poster relative-position">
      <q-skeleton v-if="!posterLoaded" type="rect" class="fit" />
      <div v-else-if="!posterInfo?.url" class="fit column items-center justify-center text-grey-6">
        <q-icon name="video_library" size="42px" /><span class="text-caption q-mt-sm">暂无封面</span>
      </div>
      <q-img v-else :src="getUrl(posterInfo.url)" class="bg-grey-2" no-spinner fit="cover" />
    </div>
    <div class="media-card__body">
      <div class="media-card__title" :title="data.name">{{ data.name }}</div>
      <div class="media-card__actions">
        <q-chip v-if="detailLoading" dense color="grey-2" text-color="grey-7">读取中</q-chip>
        <dialog-t-v-detail v-else-if="detailInfo" :data="detailInfo">
          <q-btn
            v-if="hasSubtitleVideoCount > 0"
            color="primary"
            flat
            dense
            icon="closed_caption"
            :label="`${hasSubtitleVideoCount}/${episodeCount}`"
            title="查看剧集字幕"
          />
          <q-btn v-else color="grey-7" flat dense icon="closed_caption_off" label="0" title="暂无字幕" />
        </dialog-t-v-detail>
        <q-space /><span class="text-caption text-grey-7">{{ episodeCount }} 集</span>
      </div>
    </div>
  </q-card>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue';
import DialogTVDetail from 'pages/library/tvs/DialogTVDetail';
import LibraryApi from 'src/api/LibraryApi';
import { getUrl, subtitleUploadList } from 'pages/library/use-library';

const props = defineProps({ data: { type: Object, required: true } });
const posterInfo = ref(null);
const posterLoaded = ref(false);
const detailInfo = ref(null);
const detailLoading = ref(true);

const getPosterInfo = async () => {
  const [response] = await LibraryApi.getTvPoster({
    name: props.data.name,
    main_root_dir_f_path: props.data.main_root_dir_f_path,
    root_dir_path: props.data.root_dir_path,
  });
  posterInfo.value = response;
  posterLoaded.value = true;
};
const getDetailInfo = async () => {
  detailLoading.value = true;
  const [response] = await LibraryApi.getTvDetail({
    name: props.data.name,
    main_root_dir_f_path: props.data.main_root_dir_f_path,
    root_dir_path: props.data.root_dir_path,
  });
  detailInfo.value = response;
  detailLoading.value = false;
};
const episodeCount = computed(() => detailInfo.value?.one_video_info?.length || 0);
const hasSubtitleVideoCount = computed(
  () => detailInfo.value?.one_video_info?.filter((episode) => episode.sub_f_path_list?.length > 0).length || 0
);

watch(subtitleUploadList, (value, oldValue) => {
  const episodePaths = detailInfo.value?.one_video_info?.map((episode) => episode.video_f_path) || [];
  const oldPaths = oldValue.map((item) => item.video_f_path);
  const currentPaths = value.map((item) => item.video_f_path);
  if (episodePaths.some((path) => oldPaths.includes(path)) && !episodePaths.some((path) => currentPaths.includes(path)))
    getDetailInfo();
});
onMounted(() => {
  getPosterInfo();
  getDetailInfo();
});
</script>
