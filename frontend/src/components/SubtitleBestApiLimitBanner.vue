<template>
  <q-banner v-if="apiLimitInfo" dense rounded class="status-strip q-mb-md" :class="limitClass">
    <template #avatar><q-icon :name="isExhausted ? 'error_outline' : 'data_usage'" /></template>
    <div>
      SubtitleBest 今日用量：<strong>{{ apiLimitInfo.dailyCount }} / {{ apiLimitInfo.dailyLimit }}</strong>
      · API Key 有效期至
      {{ dayjs.unix(apiLimitInfo.expireTime).format('YYYY-MM-DD HH:mm:ss') }}
    </div>
  </q-banner>
</template>

<script setup>
import { computed, ref } from 'vue';
import useEventBus from 'src/composables/use-event-bus';
import dayjs from 'dayjs';

const apiLimitInfo = ref(null);
const usageRatio = computed(
  () => Number(apiLimitInfo.value?.dailyCount || 0) / Number(apiLimitInfo.value?.dailyLimit || 1)
);
const isExhausted = computed(() => usageRatio.value >= 1);
const limitClass = computed(() => {
  if (isExhausted.value) return 'status-strip--danger';
  if (usageRatio.value >= 0.8) return 'status-strip--warning';
  return '';
});

useEventBus('subtitle-best-api-limit-info', (info) => {
  apiLimitInfo.value = info;
});
</script>
