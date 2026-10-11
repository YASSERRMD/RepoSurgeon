import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHistory } from 'vue-router';

import App from './App.vue';
import { authGuard, routes } from '@/router';
import './styles/theme.css';

const router = createRouter({
  history: createWebHistory(),
  routes,
});

// Sessions are resolved once, before any guarded view is mounted.
router.beforeEach(authGuard);

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : 'RepoSurgeon';
  document.title = `${title} · RepoSurgeon`;
});

const app = createApp(App);
app.use(createPinia());
app.use(router);
app.mount('#app');
