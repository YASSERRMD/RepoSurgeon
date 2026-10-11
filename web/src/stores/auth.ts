import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, setCsrfToken, type HealthClient } from '@/api/client';
import type { CurrentUser, MembershipSummary, Permission } from '@/api/types';
import {
  errorMessage,
  readStoredActiveOrganization,
  writeStoredActiveOrganization,
} from '@/stores/organizations';

/**
 * unknown means the session has not been probed yet: the router guard runs
 * bootstrap() once before it decides whether a redirect to /login is needed.
 */
export type AuthStatus = 'unknown' | 'authenticated' | 'anonymous';

export const useAuthStore = defineStore('auth', () => {
  const client: HealthClient = createHealthClient();
  const user = ref<CurrentUser | null>(null);
  const activeOrganizationId = ref<string | null>(readStoredActiveOrganization());
  const status = ref<AuthStatus>('unknown');
  const loading = ref(false);
  const error = ref<string | null>(null);
  /** csrfToken mirrors the client's in-memory value; it is never persisted. */
  const csrfToken = ref<string | null>(null);

  let bootstrapping: Promise<void> | null = null;

  const isAuthenticated = computed<boolean>(() => status.value === 'authenticated');

  const activeOrganization = computed<MembershipSummary | null>(() => {
    if (user.value === null || activeOrganizationId.value === null) return null;
    return user.value.organizations.find((item) => item.id === activeOrganizationId.value) ?? null;
  });

  /** permissionsFor lists what the caller may do inside one organization. */
  function permissionsFor(organizationId: string): Permission[] {
    const membership = user.value?.organizations.find((item) => item.id === organizationId) ?? null;
    return membership === null ? [] : [...membership.permissions];
  }

  /** can checks the permission against the ACTIVE organization only. */
  function can(permission: Permission): boolean {
    if (activeOrganizationId.value === null) return false;
    return permissionsFor(activeOrganizationId.value).includes(permission);
  }

  function applyUser(currentUser: CurrentUser): void {
    user.value = currentUser;
    status.value = 'authenticated';
    error.value = null;
    const memberships = currentUser.organizations;
    const known = memberships.some((membership) => membership.id === activeOrganizationId.value);
    if (!known) {
      setActiveOrganizationId(memberships[0]?.id ?? null);
    }
  }

  function clearSession(): void {
    user.value = null;
    status.value = 'anonymous';
    csrfToken.value = null;
    setCsrfToken(null);
  }

  function setActiveOrganizationId(id: string | null): void {
    activeOrganizationId.value = id;
    writeStoredActiveOrganization(id);
  }

  /**
   * login exchanges credentials for a session cookie, then loads the full
   * profile so roles and permissions are known for every organization.
   */
  async function login(email: string, password: string): Promise<CurrentUser> {
    loading.value = true;
    error.value = null;
    try {
      const session = await client.login({ email: email.trim(), password });
      csrfToken.value = session.csrf_token;
      setCsrfToken(session.csrf_token);
      const currentUser = await client.getCurrentUser();
      applyUser(currentUser);
      return currentUser;
    } catch (cause) {
      clearSession();
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      loading.value = false;
    }
  }

  /** logout revokes the session server-side; local state clears either way. */
  async function logout(): Promise<void> {
    loading.value = true;
    let failure: string | null = null;
    try {
      await client.logout();
    } catch (cause) {
      failure = errorMessage(cause);
    }
    clearSession();
    error.value = failure;
    loading.value = false;
  }

  /** refresh reloads the caller; any failure means the session is gone. */
  async function refresh(): Promise<void> {
    loading.value = true;
    let profile: CurrentUser;
    try {
      profile = await client.getCurrentUser();
    } catch {
      clearSession();
      loading.value = false;
      return;
    }
    applyUser(profile);
    loading.value = false;

    // After a page reload the browser keeps the session cookie but has no
    // CSRF token, so every mutation would fail. Session rotation is exempt
    // from the CSRF check precisely for this case; it mints a fresh token. A
    // failure here leaves the session valid.
    if (csrfToken.value === null) {
      try {
        await rotateSession();
      } catch {
        // Convenience only: CSRF-protected mutations stay unavailable until
        // the next successful rotation.
      }
    }
  }

  /** rotateSession swaps the session token and the CSRF token bound to it. */
  async function rotateSession(): Promise<string> {
    loading.value = true;
    error.value = null;
    try {
      const session = await client.refreshSession();
      csrfToken.value = session.csrf_token;
      setCsrfToken(session.csrf_token);
      return session.expires_at;
    } catch (cause) {
      // A failed rotation does not invalidate the cookie session, so the user
      // stays signed in; only the CSRF-protected calls need another attempt.
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      loading.value = false;
    }
  }

  /**
   * bootstrap is the router guard's entry point: probe the session once, and
   * keep concurrent navigations from probing it twice.
   */
  async function bootstrap(): Promise<void> {
    if (status.value !== 'unknown') return;
    if (bootstrapping !== null) return bootstrapping;
    bootstrapping = refresh().finally(() => {
      bootstrapping = null;
    });
    return bootstrapping;
  }

  /** selectOrganization switches the scope used by can() and the data views. */
  function selectOrganization(organizationId: string): void {
    if (!isMember(organizationId)) return;
    setActiveOrganizationId(organizationId);
  }

  function isMember(organizationId: string): boolean {
    return (
      user.value?.organizations.some((membership) => membership.id === organizationId) ?? false
    );
  }

  return {
    client,
    user,
    activeOrganizationId,
    activeOrganization,
    status,
    loading,
    error,
    csrfToken,
    isAuthenticated,
    permissionsFor,
    can,
    login,
    logout,
    refresh,
    rotateSession,
    bootstrap,
    selectOrganization,
    isMember,
  };
});
