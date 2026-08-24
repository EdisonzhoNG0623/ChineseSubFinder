<template>
  <q-card flat class="media-card">
    <div class="media-card__poster relative-position">
      <q-skeleton v-if="!posterLoaded" type="rect" class="fit" />
      <div v-else-if="!posterInfo.url" class="fit column items-center justify-center text-grey-6">
        <q-icon name="movie" size="42px" />
        <span class="text-caption q-mt-sm">暂无封面</span>
      </div>
      <q-img v-else :src="getUrl(posterInfo.url)" class="bg-grey-2" no-spinner fit="cover" />
    </div>
    <div class="media-card__body">
      <div class="media-card__title" :title="data.name">{{ data.name }}</div>
      <div class="media-card__actions">
        <btn-dialog-preview-video
          v-if="hasSubtitle"
          size="sm"
          :subtitle-url-list="detialInfo?.sub_url_list"
          :path="data.video_f_path"
        />

        <div>
          <q-btn
            v-if="hasSubtitle"
            size="sm"
            color="black"
            round
            flat
            dense
            icon="closed_caption"
            @click.stop
            title="已有字幕"
          >
            <q-popup-proxy>
              <q-list dense>
                <q-item v-for="(item, index) in detialInfo.sub_url_list" :key="item">
                  <q-item-section side>{{ index + 1 }}.</q-item-section>

                  <q-item-section class="overflow-hidden ellipsis" :title="item.split`(/\/|\\/)`.pop()">
                    <a class="text-primary" :href="getUrl(item)" target="_blank" rel="noopener noreferrer">{{
                      item.split(/\/|\\/).pop()
                    }}</a>
                  </q-item-section>
                  <q-item-section side>
                    <q-btn
                      color="primary"
                      round
                      flat
                      dense
                      icon="construction"
                      :title="`字幕时间轴校准${
                        !formModel.advanced_settings.fix_time_line
                          ? '（请先在“匹配与队列”中开启自动校正字幕时间轴）'
                          : ''
                      }`"
                      @click="doFixSubtitleTimeline(item)"
                      :disable="!formModel.advanced_settings.fix_time_line"
                    ></q-btn>
                  </q-item-section>
                </q-item>
              </q-list>
            </q-popup-proxy>
          </q-btn>
          <q-btn v-else color="grey" size="sm" round flat dense icon="closed_caption" @click.stop title="没有字幕" />
        </div>

        <btn-dialog-search-subtitle :path="props.data.video_f_path" is-movie />
        <q-space />

        <btn-upload-subtitle :path="data.video_f_path" dense size="sm" />

        <q-btn
          class="btn-download"
          color="primary"
          round
          flat
          dense
          icon="download_for_offline"
          title="添加到下载队列"
          @click="downloadSubtitle"
          size="sm"
        ></q-btn>

        <div>
          <btn-ignore-video :path="props.data.video_f_path" :video-type="VIDEO_TYPE_MOVIE" size="sm" />
        </div>
      </div>
    </div>
  </q-card>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue';
import LibraryApi from 'src/api/LibraryApi';
import { SystemMessage } from 'src/utils/message';
import { VIDEO_TYPE_MOVIE } from 'src/constants/SettingConstants';
import { useQuasar } from 'quasar';
import { doFixSubtitleTimeline, getUrl, subtitleUploadList } from 'pages/library/use-library';
import BtnIgnoreVideo from 'pages/library/BtnIgnoreVideo';
import BtnUploadSubtitle from 'pages/library/BtnUploadSubtitle';
import BtnDialogPreviewVideo from 'pages/library/BtnDialogPreviewVideo';
import BtnDialogSearchSubtitle from 'pages/library/BtnDialogSearchSubtitle';
import { formModel } from 'pages/settings/use-settings';

const props = defineProps({
  data: Object,
});

const $q = useQuasar();

const posterInfo = ref(null);
const posterLoaded = ref(false);
const detialInfo = ref(null);

const getPosterInfo = async () => {
  const [res] = await LibraryApi.getMoviePoster({
    name: props.data.name,
    main_root_dir_f_path: props.data.main_root_dir_f_path,
    video_f_path: props.data.video_f_path,
  });
  posterInfo.value = res;
  posterLoaded.value = true;
};

const getDetailInfo = async () => {
  const [res] = await LibraryApi.getMovieDetail({
    name: props.data.name,
    main_root_dir_f_path: props.data.main_root_dir_f_path,
    video_f_path: props.data.video_f_path,
  });
  detialInfo.value = res;
};

const hasSubtitle = computed(() => detialInfo.value?.sub_url_list.length > 0);

const downloadSubtitle = async () => {
  $q.dialog({
    title: '添加到下载队列',
    message: '选择下载任务的类型：',
    options: {
      model: 3,
      type: 'radio',
      items: [
        { label: '插队任务', value: 3 },
        { label: '一次性任务（执行成功后忽略该任务）', value: 0 },
      ],
    },
    cancel: true,
    persistent: true,
  }).onOk(async (val) => {
    const [, err] = await LibraryApi.downloadSubtitle({
      video_type: VIDEO_TYPE_MOVIE,
      physical_video_file_full_path: props.data.video_f_path,
      task_priority_level: val, // 一般的队列等级是5，如果想要快，那么可以先默认这里填写3，这样就可以插队
      // 媒体服务器内部视频ID  `video/list` 中 获取到的 media_server_inside_video_id，可以用于自动 Emby 字幕列表刷新用
      media_server_inside_video_id: props.data.media_server_inside_video_id,
    });
    if (err !== null) {
      SystemMessage.error(err.message);
    } else {
      SystemMessage.success('已加入下载队列');
    }
  });
};

watch(subtitleUploadList, (val, oldVal) => {
  // 上传字幕列表当前文件有变化时刷新
  if (
    (val.find((e) => e.video_f_path === props.data.video_f_path) &&
      !oldVal.find((e) => e.video_f_path === props.data.video_f_path)) ||
    (!val.find((e) => e.video_f_path === props.data.video_f_path) &&
      oldVal.find((e) => e.video_f_path === props.data.video_f_path))
  ) {
    getDetailInfo();
  }
});

onMounted(() => {
  getPosterInfo();
  getDetailInfo();
});
</script>
