<template>
  <div class="settings-panel">
    <q-list dense class="settings-form-list">
      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>Bark 接口失效通知</q-item-label>
          <q-item-label caption>可选的维护告警；普通用户保持关闭即可</q-item-label>
          <div v-if="form.enable" class="q-mt-sm">
            <q-input v-model="form.bark_server_address" outlined dense label="Bark Server 地址" />
          </div>
        </q-item-section>
        <q-item-section avatar top>
          <q-toggle v-model="form.enable" />
        </q-item-section>
      </q-item>
    </q-list>

    <q-separator class="q-mt-md" />
  </div>
</template>

<script setup>
import { formModel } from 'pages/settings/use-settings';
import { toRefs } from '@vueuse/core';

const { developer_settings: form } = toRefs(formModel);
</script>
