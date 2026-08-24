<template>
  <q-btn
    v-if="compact"
    class="toolbar-feedback"
    flat
    round
    dense
    icon="bug_report"
    aria-label="反馈问题"
    @click="gotoGithubIssuePage"
  >
    <q-tooltip>反馈问题</q-tooltip>
  </q-btn>
  <q-item v-else clickable @click="gotoGithubIssuePage" title="跳转到 GitHub issue 页面">
    <q-item-section>
      <div class="row items-center">
        <span><q-icon name="bug_report" size="18px" /></span>
        <span class="q-ml-xs">问题反馈</span>
      </div>
    </q-item-section>
  </q-item>
</template>

<script setup>
import { gotoGithubIssuePage } from 'src/utils/common';

defineProps({ compact: Boolean });
</script>
