import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHistory } from 'vue-router';

import App from './App.vue';
import HomeView from './views/HomeView.vue';
import OrganizationsView from './views/OrganizationsView.vue';
import ProjectsView from './views/ProjectsView.vue';
import ProjectDetailView from './views/ProjectDetailView.vue';
import './styles/theme.css';

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: HomeView,
      meta: { title: 'Operational status' },
    },
    {
      path: '/organizations',
      name: 'organizations',
      component: OrganizationsView,
      meta: { title: 'Organizations' },
    },
    {
      path: '/projects',
      name: 'projects',
      component: ProjectsView,
      meta: { title: 'Projects' },
    },
    {
      path: '/projects/:projectId',
      name: 'project-detail',
      component: ProjectDetailView,
      meta: { title: 'Project' },
    },
  ],
});

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : 'RepoSurgeon';
  document.title = `${title} · RepoSurgeon`;
});

const app = createApp(App);
app.use(createPinia());
app.use(router);
app.mount('#app');
