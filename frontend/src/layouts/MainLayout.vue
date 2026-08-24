<template>
  <q-layout view="hHh LpR fFf" class="app-shell">
    <a class="skip-link" href="#main-content">跳到主要内容</a>

    <q-header class="app-header">
      <q-toolbar class="app-toolbar">
        <q-btn flat dense round icon="menu" aria-label="切换导航栏" @click="leftDrawerOpen = !leftDrawerOpen" />
        <div class="app-toolbar__title">
          <div class="app-toolbar__eyebrow">CHINESE SUB FINDER</div>
          <div class="app-toolbar__page">{{ $route.meta.title }}</div>
        </div>
        <q-space />
        <div class="app-toolbar__actions row items-center no-wrap">
          <q-chip
            class="toolbar-task-status"
            dense
            square
            :color="isJobRunning ? 'positive' : 'grey-4'"
            :text-color="isJobRunning ? 'white' : 'grey-8'"
          >
            <q-icon :name="isJobRunning ? 'sync' : 'pause'" size="15px" class="q-mr-xs" />
            {{ isJobRunning ? '自动任务运行中' : '自动任务已暂停' }}
          </q-chip>
          <version-update-item>
            <q-btn flat round dense icon="system_update_alt" aria-label="查看版本更新">
              <q-tooltip>版本更新</q-tooltip>
            </q-btn>
          </version-update-item>
          <q-btn
            class="toolbar-help"
            flat
            round
            dense
            icon="help_outline"
            aria-label="打开帮助文档"
            @click="openPage(helpUrl)"
          >
            <q-tooltip>帮助文档</q-tooltip>
          </q-btn>
          <bug-report-item compact />
          <q-btn-dropdown
            flat
            no-caps
            content-class="user-menu"
            :label="userState.username || '管理员'"
            icon="account_circle"
          >
            <q-list padding style="min-width: 160px">
              <q-item-label header>当前账号</q-item-label>
              <q-item clickable v-close-popup @click="logout">
                <q-item-section avatar><q-icon name="logout" /></q-item-section>
                <q-item-section>退出登录</q-item-section>
              </q-item>
            </q-list>
          </q-btn-dropdown>
        </div>
      </q-toolbar>
    </q-header>

    <q-drawer v-model="leftDrawerOpen" class="app-sidebar" :breakpoint="900" :width="264" show-if-above>
      <div class="column fit no-wrap">
        <div class="app-brand">
          <div class="app-brand__mark"><img src="icons/logo.png" alt="ChineseSubFinder 标志" /></div>
          <div class="ellipsis">
            <div class="app-brand__name">ChineseSubFinder</div>
            <div class="app-brand__version">{{ systemState.systemInfo?.version || '正在读取版本' }}</div>
          </div>
        </div>
        <div class="app-nav-label">工作区</div>
        <q-list class="app-nav">
          <menu-item v-for="route in menus" :menu-info="route" :key="`${route.name}${route.path}`" />
        </q-list>
        <div class="app-sidebar__footer">
          <div class="row items-center q-gutter-xs text-white q-mb-xs">
            <q-icon name="verified_user" size="16px" />
            <span class="text-weight-medium">本地媒体工作台</span>
          </div>
          配置和密钥仅保存在你的实例中。供应商检测不会记录媒体路径或密钥。
        </div>
      </div>
    </q-drawer>

    <q-page-container id="main-content" tabindex="-1">
      <router-view />
      <notice-dialog />
    </q-page-container>
  </q-layout>
</template>

<script setup>
import routes from 'src/router/routes';
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { LocalStorage } from 'quasar';
import MenuItem from 'layouts/MenuItem';
import BugReportItem from 'layouts/BugReportItem';
import VersionUpdateItem from 'components/VersionUpdateItem';
import NoticeDialog from 'components/NoticeDialog';
import AccessApi from 'src/api/AccessApi';
import { isJobRunning, systemState } from 'src/store/systemState';
import { userState } from 'src/store/userState';

const router = useRouter();
const leftDrawerOpen = ref(false);
const menus = routes.find((route) => route.path === '/').children;
const helpUrl = 'https://github.com/ChineseSubFinder/ChineseSubFinder/blob/master/docker/readme.md';

const logout = () => {
  userState.username = '';
  userState.accessToken = undefined;
  LocalStorage.remove('token');
  AccessApi.logout();
  router.push('/access/login');
};

const openPage = (url) => window.open(url, '_blank', 'noopener,noreferrer');
</script>
