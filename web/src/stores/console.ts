import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type { DashboardView, PreferencesSaveRequest, PreferencesView } from '@/api/types';
import { failureOf } from './context';

/**
 * The console store owns the aggregate metrics and the operator's view
 * settings. The dashboard numbers arrive from the server, so a failure keeps
 * the last reading visible and reports the error rather than inventing counts.
 */
export const useConsoleStore = defineStore('console', () => {
  const client: HealthClient = createHealthClient();
  const dashboard = ref<DashboardView | null>(null);
  const preferences = ref<PreferencesView | null>(null);
  const loading = ref(false);
  const saving = ref(false);
  const error = ref<string | null>(null);

  const runsByState = computed<Record<string, number>>(() => dashboard.value?.runs_by_state ?? {});
  const recentRuns = computed(() => dashboard.value?.recent_runs ?? []);
  const blockedRuns = computed<number>(() => dashboard.value?.blocked_runs ?? 0);
  const failedRuns = computed<number>(() => dashboard.value?.failed_runs ?? 0);
  const openIssues = computed<number>(() => dashboard.value?.open_issues ?? 0);
  const recentAuditEvents = computed<number>(() => dashboard.value?.recent_audit_events ?? 0);

  async function loadDashboard(): Promise<void> {
    loading.value = true;
    try {
      dashboard.value = await client.getDashboard();
      error.value = null;
    } catch (cause) {
      error.value = failureOf(cause).message;
    } finally {
      loading.value = false;
    }
  }

  async function loadPreferences(): Promise<void> {
    try {
      preferences.value = await client.getPreferences();
      error.value = null;
    } catch (cause) {
      error.value = failureOf(cause).message;
    }
  }

  /**
   * savePreferences stores the settings document. The server validates it, so a
   * rejection is reported with its own message instead of being retried blindly.
   */
  async function savePreferences(view: Record<string, unknown>): Promise<boolean> {
    saving.value = true;
    try {
      const body: PreferencesSaveRequest = { view };
      preferences.value = await client.savePreferences(body, newIdempotencyKey());
      error.value = null;
      return true;
    } catch (cause) {
      error.value = failureOf(cause).message;
      return false;
    } finally {
      saving.value = false;
    }
  }

  return {
    dashboard,
    preferences,
    loading,
    saving,
    error,
    runsByState,
    recentRuns,
    blockedRuns,
    failedRuns,
    openIssues,
    recentAuditEvents,
    loadDashboard,
    loadPreferences,
    savePreferences,
  };
});

/** consoleNav is the section registry the shell renders. */
export const consoleNav = [
  { to: '/', label: 'Overview', section: 'overview', permission: 'project.read' },
  { to: '/projects', label: 'Projects', section: 'projects', permission: 'project.read' },
  { to: '/issues', label: 'Issues', section: 'issues', permission: 'project.read' },
  { to: '/runs', label: 'Runs', section: 'runs', permission: 'project.read' },
  { to: '/workspaces', label: 'Workspaces', section: 'workspaces', permission: 'project.read' },
  { to: '/models', label: 'Models', section: 'models', permission: 'project.read' },
  { to: '/approvals', label: 'Approvals', section: 'approvals', permission: 'approval.decide' },
  { to: '/audit', label: 'Audit', section: 'audit', permission: 'project.read' },
  { to: '/settings', label: 'Settings', section: 'settings', permission: 'project.read' },
] as const;

/** consoleAdminNav are the sections that additionally appear for members with
 * organization administration rights. */
export const consoleAdminNav = [
  {
    to: '/organizations',
    label: 'Organizations',
    section: 'organizations',
    permission: 'member.admin',
  },
  { to: '/members', label: 'Members', section: 'members', permission: 'member.admin' },
  {
    to: '/credentials',
    label: 'Credentials',
    section: 'credentials',
    permission: 'credential.admin',
  },
] as const;
