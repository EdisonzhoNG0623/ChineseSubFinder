import RouterPlaceholder from 'components/RouterPlaceholder';

const routes = [
  {
    path: '/',
    component: () => import('layouts/MainLayout.vue'),
    redirect: { name: 'overview' },
    children: [
      {
        name: 'overview',
        path: 'overview',
        component: () => import('pages/overview/index.vue'),
        meta: { title: '运行总览', icon: 'space_dashboard' },
      },
      {
        name: 'library',
        path: 'library',
        component: RouterPlaceholder,
        meta: { title: '媒体库', icon: 'video_library' },
        children: [
          {
            name: 'library.movie.list',
            path: 'movies',
            component: () => import('pages/library/movies/index.vue'),
            meta: { title: '电影', icon: 'movie' },
          },
          {
            name: 'library.tv.list',
            path: 'tvs',
            component: () => import('pages/library/tvs/index.vue'),
            meta: { title: '连续剧', icon: 'live_tv' },
          },
        ],
      },
      { path: 'library/library/movies', redirect: { name: 'library.movie.list' } },
      { path: 'library/library/tvs', redirect: { name: 'library.tv.list' } },
      {
        name: 'jobs',
        path: 'jobs',
        component: () => import('pages/jobs/index.vue'),
        meta: { title: '下载队列', icon: 'format_list_bulleted' },
      },
      {
        name: 'suppliers',
        path: 'suppliers',
        component: () => import('pages/suppliers/index.vue'),
        meta: { title: '字幕源状态', icon: 'hub' },
      },
      {
        name: 'settings',
        path: 'settings',
        component: () => import('pages/settings/index.vue'),
        meta: { title: '系统设置', icon: 'tune' },
      },
    ],
  },

  {
    path: '/access',
    component: RouterPlaceholder,
    children: [
      {
        path: 'login',
        component: () => import('pages/access/login/index.vue'),
      },
    ],
  },

  {
    path: '/setup',
    component: () => import('pages/setup/index.vue'),
  },

  // Always leave this as last one,
  // but you can also remove it
  {
    path: '/:catchAll(.*)*',
    component: () => import('pages/Error404.vue'),
  },
];

export default routes;
