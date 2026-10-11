import type { RouteLocationNormalized, RouteLocationRaw, RouteRecordRaw } from 'vue-router';

import ApprovalsView from '@/views/ApprovalsView.vue';
import ContextInspectorView from '@/views/ContextInspectorView.vue';
import CredentialsView from '@/views/CredentialsView.vue';
import HomeView from '@/views/HomeView.vue';
import LoginView from '@/views/LoginView.vue';
import MembersView from '@/views/MembersView.vue';
import OrganizationsView from '@/views/OrganizationsView.vue';
import PlanView from '@/views/PlanView.vue';
import ProjectDetailView from '@/views/ProjectDetailView.vue';
import ProjectsView from '@/views/ProjectsView.vue';
import ToolsView from '@/views/ToolsView.vue';
import WorkspaceSearchView from '@/views/WorkspaceSearchView.vue';
import { useAuthStore } from '@/stores/auth';

/**
 * Application routes require an authenticated session by default. The
 * operational status page (/) and /login are public: readiness must stay
 * visible to operators and to the login form itself.
 */
export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: HomeView,
    meta: { title: 'Operational status', requiresAuth: false },
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
    path: '/approvals',
    name: 'approvals',
    component: ApprovalsView,
    meta: { title: 'Approvals', requiresAuth: true },
  },
  {
    path: '/tools',
    name: 'tools',
    component: ToolsView,
    meta: { title: 'Tools', requiresAuth: true },
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
  {
    // The bare paths are the navigation entry points: the identifier is not
    // known until the operator supplies it, and the view asks for it.
    path: '/workspaces',
    name: 'workspaces',
    component: WorkspaceSearchView,
    meta: { title: 'Code search', requiresAuth: true },
  },
  {
    path: '/workspaces/:workspaceId',
    name: 'workspace-search',
    component: WorkspaceSearchView,
    meta: { title: 'Code search', requiresAuth: true },
  },
  {
    path: '/runs',
    name: 'runs',
    component: ContextInspectorView,
    meta: { title: 'Context inspector', requiresAuth: true },
  },
  {
    path: '/runs/:runId/context',
    name: 'context-inspector',
    component: ContextInspectorView,
    meta: { title: 'Context inspector', requiresAuth: true },
  },
  {
    // The plan a run proposed is readable by any member; deciding it is gated
    // by the approval.decide permission inside the view and by the API.
    path: '/runs/:runId/plan',
    name: 'plan-inspector',
    component: PlanView,
    meta: { title: 'Plan inspector', requiresAuth: true },
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
