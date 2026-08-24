<template>
  <q-btn dense flat color="primary" label="修改" @click="visible = true"></q-btn>
  <common-form-dialog
    :title="title"
    v-model:visible="visible"
    :submit-fn="handleSubmit"
    @before-show="handleBeforeShow"
  >
    <q-input
      v-model.trim="form.url"
      label="入口地址"
      outlined
      hint="填写协议和域名；路径由系统自动拼接"
      :rules="urlRules"
    >
      <template v-slot:after>
        <q-btn v-if="data.name !== 'a4k'" dense flat label="恢复默认" color="primary" @click="resetUrl" />
      </template>
    </q-input>
    <q-input
      v-if="data.name !== 'csf'"
      v-model.number="form.dailyLimit"
      type="number"
      label="每日下载上限"
      hint="0 表示停用，-1 表示不限；正数为每天最多成功下载次数"
      :rules="[(val) => val !== '' || '不能为空']"
      outlined
    />
  </common-form-dialog>
</template>

<script setup>
import CommonFormDialog from 'components/CommonFormDialog';
import { computed, reactive, ref } from 'vue';
import { DEFAULT_SUB_SOURCE_URL_MAP } from 'src/constants/SettingConstants';

const props = defineProps({
  data: Object,
});

const emit = defineEmits(['update']);

const form = reactive({
  url: '',
  dailyLimit: -1,
});

const visible = ref(false);

const title = computed(() => `修改字幕源：${props.data?.name}`);
const urlRules = [
  (value) => !!value || '请输入入口地址',
  (value) => /^https?:\/\//i.test(value) || '地址必须以 http:// 或 https:// 开头',
];

const handleSubmit = () => {
  emit('update', form);
  visible.value = false;
};

const handleBeforeShow = () => {
  form.url = props.data?.root_url;
  form.dailyLimit = props.data?.daily_download_limit;
};

const resetUrl = () => {
  form.url = DEFAULT_SUB_SOURCE_URL_MAP[props.data?.name];
};
</script>
