<template>
  <q-layout view="lHh Lpr fff">
    <q-page-container>
      <q-page class="auth-page">
        <section class="auth-story">
          <div class="auth-brand"><img src="icons/logo.png" alt="" /><span>ChineseSubFinder</span></div>
          <div>
            <div class="eyebrow">SUBTITLE OPERATIONS</div>
            <h1>让字幕任务保持<br />可见、可控、可恢复。</h1>
            <p>集中管理媒体库、下载队列、字幕源健康和 AI 识别策略。</p>
          </div>
          <div class="auth-story__status"><q-icon name="lock" /> 管理面板仅在你的实例中运行</div>
        </section>

        <section class="auth-form-wrap" aria-labelledby="login-title">
          <q-form class="auth-card" @submit="submit">
            <div class="eyebrow">WELCOME BACK</div>
            <h2 id="login-title">登录管理面板</h2>
            <p class="text-grey-7 q-mb-xl">使用初始化时创建的管理员账号。</p>
            <q-input
              v-model="form.username"
              outlined
              autocomplete="username"
              label="用户名"
              lazy-rules
              :rules="[(value) => !!value || '请输入用户名']"
            >
              <template #prepend><q-icon name="person_outline" /></template>
            </q-input>
            <q-input
              v-model="form.password"
              outlined
              :type="showPassword ? 'text' : 'password'"
              autocomplete="current-password"
              label="密码"
              lazy-rules
              :rules="[(value) => !!value || '请输入密码']"
            >
              <template #prepend><q-icon name="lock_outline" /></template>
              <template #append
                ><q-btn
                  flat
                  round
                  dense
                  :icon="showPassword ? 'visibility_off' : 'visibility'"
                  :aria-label="showPassword ? '隐藏密码' : '显示密码'"
                  @click="showPassword = !showPassword"
              /></template>
            </q-input>
            <q-btn
              unelevated
              color="primary"
              type="submit"
              :loading="submitting"
              class="full-width q-mt-md"
              size="lg"
              label="登录"
            />
          </q-form>
        </section>
      </q-page>
    </q-page-container>
  </q-layout>
</template>

<script setup>
import { reactive, ref } from 'vue';
import { LocalStorage } from 'quasar';
import { useRouter } from 'vue-router';
import { SystemMessage } from 'src/utils/message';
import AccessApi from 'src/api/AccessApi';
import { userState } from 'src/store/userState';

const router = useRouter();
const form = reactive({ username: '', password: '' });
const submitting = ref(false);
const showPassword = ref(false);

const submit = async () => {
  submitting.value = true;
  const [response, error] = await AccessApi.login({ ...form });
  submitting.value = false;
  if (error) {
    SystemMessage.error(error.message);
    return;
  }
  const userData = { accessToken: response.access_token, username: form.username };
  Object.assign(userState, userData);
  LocalStorage.set('token', userData);
  router.push('/');
};
</script>
