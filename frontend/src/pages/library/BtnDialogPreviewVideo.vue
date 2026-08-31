<template>
  <q-btn color="primary" icon="smart_display" flat dense v-bind="$attrs" @click="handleBtnClick" title="预览" />

  <q-dialog
    v-model="visible"
    persistent
    transition-show="slide-up"
    transition-hide="slide-down"
    maximized
    @before-show="handleBeforeShow"
    @before-hide="handleBeforeHide"
  >
    <q-card class="column">
      <q-bar>
        <div class="text-bold">{{ path.split(/\/|\\/).pop() }}</div>
        <q-space />
        <q-btn dense flat icon="close" v-close-popup title="关闭" />
      </q-bar>

      <q-card-section class="col column items-center justify-center no-wrap" v-if="checkResult">
        <div class="q-pa-md justify-center" v-if="checkResult.message === undefined">
          <div class="text-bold q-mb-sm cursor-pointer" @click="showSelectSubtitleDialog">
            字幕：{{ selectedSub?.split(/\/|\\/).pop() }}
          </div>
          <artplayer :option="artOption" class="video-preview-player" @get-instance="handleGetArtInstance"></artplayer>
        </div>

        <div class="row items-center" v-else>
          <q-icon name="error" size="4rem" color="red" />
          <div class="text-h6 text-red">{{ checkResult.message }}</div>
        </div>
      </q-card-section>
    </q-card>
  </q-dialog>
</template>

<script setup>
import { computed, ref } from 'vue';
import Hls from 'hls.js';
import { encode } from 'js-base64';
import LibraryApi from 'src/api/LibraryApi';
import Artplayer from 'components/Artplayer';
import { useQuasar } from 'quasar';
import { getBackendUrl, getUrl, isCrossOriginResourceUrl } from 'pages/library/use-library';
import { userState } from 'src/store/userState';
import { requestAccessToken } from 'src/utils/http/authorization';

const $q = useQuasar();

const props = defineProps({
  path: String,
  onBtnClick: Function,
  subtitleUrlList: {
    type: Array,
    default: () => [],
  },
  subtitleType: String,
});

const visible = ref(false);
const artInstance = ref(null);
const selectedSub = ref(null);
const checkResult = ref(null);
const playlistTicket = ref(null);

const handleBtnClick = async () => {
  if (props.onBtnClick) {
    props.onBtnClick((flag) => {
      if (flag) {
        visible.value = true;
      }
    });
  } else {
    visible.value = true;
  }
};

const handleGetArtInstance = (instance) => {
  artInstance.value = instance;
};

const showSelectSubtitleDialog = () => {
  $q.dialog({
    title: '选择字幕',
    options: {
      type: 'radio',
      model: selectedSub.value,
      items: props.subtitleUrlList.map((e) => ({ label: e, value: e })),
    },
    cancel: true,
    persistent: true,
  }).onOk(async (data) => {
    selectedSub.value = data;
    artInstance.value.subtitle.switch(getUrl(data));
  });
};

const playlistUrl = computed(() => {
  const path = `/v1/preview/playlist/${encode(encodeURIComponent(props.path))}`;
  const url = getBackendUrl(path);
  if (!playlistTicket.value || !isCrossOriginResourceUrl(url)) return url;
  return `${url}?hls_ticket=${encodeURIComponent(playlistTicket.value)}`;
});

const artOption = computed(() => {
  const options = {
    autoplay: true,
    autoSize: true,
    url: playlistUrl.value,
    subtitle: {
      url: selectedSub.value.startsWith('blob') ? selectedSub.value : getUrl(selectedSub.value),
    },
    type: 'm3u8',
    customType: {
      m3u8(video, url) {
        if (Hls.isSupported()) {
          const hls = new Hls();
          hls.loadSource(url);
          hls.attachMedia(video);
        } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
          video.src = url;
        } else {
          // art.notice.show = '不支持播放格式：m3u8';
        }
      },
    },
    controls:
      props.subtitleUrlList.length === 0
        ? []
        : [
            {
              disable: false,
              name: 'button',
              index: 10,
              position: 'right',
              html: '选择字幕',
              tooltip: '选择字幕',
              style: {
                color: 'red',
              },
              click() {
                showSelectSubtitleDialog();
              },
              mounted() {
                // console.log('自定义按钮挂载完成1');
              },
            },
          ],
  };
  if (props.subtitleType) {
    options.subtitle.type = props.subtitleType;
  }
  return options;
});

const handleBeforeShow = async () => {
  selectedSub.value = props.subtitleUrlList?.[0];
  const [res, err, response] = await LibraryApi.getVideoM3u8(props.path);
  const responseToken = requestAccessToken(response?.config);
  playlistTicket.value =
    responseToken && responseToken === userState.accessToken
      ? response?.headers?.['x-csf-hls-playlist-ticket']
      : undefined;
  checkResult.value = res || err;
};

const handleBeforeHide = () => {
  checkResult.value = null;
  playlistTicket.value = null;
  LibraryApi.cleanAllPreviewJobData();
};
</script>
