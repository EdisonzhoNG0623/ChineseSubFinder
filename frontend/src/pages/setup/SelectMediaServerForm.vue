<template>
  <q-form ref="form">
    <q-list class="media-server-options">
      <q-item tag="label" v-ripple>
        <q-item-section avatar>
          <q-radio v-model="setupState.form.mediaServer" val="" />
        </q-item-section>
        <q-item-section>
          <q-item-label class="text-weight-medium">仅扫描本地目录</q-item-label>
          <q-item-label caption>不连接媒体服务器，仍可使用完整的字幕扫描与下载功能。</q-item-label>
        </q-item-section>
      </q-item>

      <q-item tag="label" v-ripple>
        <q-item-section avatar>
          <q-radio v-model="setupState.form.mediaServer" val="emby" />
        </q-item-section>
        <q-item-section>
          <q-item-label class="row items-center q-gutter-sm"
            ><img src="images/emby-logo.png" class="media-server-logo" alt="Emby" /><strong
              >连接 Emby</strong
            ></q-item-label
          >
          <q-item-label caption>同步媒体信息、观看状态和字幕列表。Jellyfin / Plex 当前没有原生连接器。</q-item-label>
        </q-item-section>
      </q-item>
    </q-list>
  </q-form>
</template>

<script setup>
import { setupState } from 'pages/setup/use-setup';
</script>

<style scoped>
.media-server-options .q-item {
  min-height: 76px;
  margin-bottom: 10px;
  border: 1px solid var(--csf-line);
  border-radius: var(--csf-radius-md);
}
.media-server-logo {
  width: auto;
  height: 28px;
  object-fit: contain;
}
</style>
