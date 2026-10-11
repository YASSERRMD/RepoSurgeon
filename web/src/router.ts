import type { RouteLocationNormalized, RouteLocationRaw, RouteRecordRaw } from 'vue-router';

import CredentialsView from '@/views/CredentialsView.vue';
import HomeView from '@/views/HomeView.vue';
import LoginView from '@/views/LoginView.vue';
import MembersView from '@/views/MembersView.vue';
import OrganizationsView from '@/views/OrganizationsView.vue';
import ProjectDetailView from '@/views/ProjectDetailView.vue';
import ProjectsView from '@/views/ProjectsView.vue';
import { useAuthStore } from '@/stores/auth';

/**
 * Every application route requires an authenticated session. /login is the
 * only route that does not: it is where an anonymous subject lands.
 */
export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: HomeView,
    meta: { title: 'Operational status', requiresAuth: true },
  },
  {
    path: '/login',
    name: 'login',
    component: LoginView,
    meta: { title: 'Sign in' },
  },
  {
    path: '/organizations',
    name: 'organizations',
    component: OrganizationsView,
    meta: { title: 'Organizations', requiresAuth: true },
  },
  {
    path: '/members',
    name: 'members',
    component: MembersView,
    meta: { title: 'Members', requiresAuth: true },
  },
  {
    path: '/credentials',
    name: 'credentials',
    component: CredentialsView,
    meta: { title: 'Credentials', requiresAuth: true },
  },
  {
    path: '/projects',
    name: 'projects',
    component: ProjectsView,
    meta: { title: 'Projects', requiresAuth: true },
  },
  {
    path: '/projects/:projectId',
    name: 'project-detail',
    component: ProjectDetailView,
    meta: { title: 'Project', requiresAuth: true },
  },
];

/**
 * authGuard resolves the session before any view runs. An unknown status
 * triggers exactly one bootstrap() probe, an anonymous subject is sent to
 * /login with the requested path in `next`, and a signed-in visitor is kept
 * away from the sign-in screen.
 */
export async function authGuard(to: RouteLocationNormalized): Promise<RouteLocationRaw | boolean> {
  const auth = useAuthStore();
  if (auth.status === 'unknown') await auth.bootstrap();

  if (to.name === 'login') {
    return auth.isAuthenticated ? { name: 'home' } : true;
  }
  if (to.meta.requiresAuth !== true) return true;
  if (!auth.isAuthenticated) {
    return { name: 'login', query: { next: to.fullPath } };
  }
  return true;
}
