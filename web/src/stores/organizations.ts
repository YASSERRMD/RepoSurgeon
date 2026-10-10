import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { ApiError, createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type { Organization } from '@/api/types';

/** localStorage key for the active organization id. No credentials are ever stored. */
export const ACTIVE_ORGANIZATION_STORAGE_KEY = 'reposurgeon.activeOrganization';

function readStoredActiveOrganization(): string | null {
  try {
    return window.localStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeStoredActiveOrganization(id: string | null): void {
  try {
    if (id === null) {
      window.localStorage.removeItem(ACTIVE_ORGANIZATION_STORAGE_KEY);
      return;
    }
    window.localStorage.setItem(ACTIVE_ORGANIZATION_STORAGE_KEY, id);
  } catch {
    // Private-mode browsers reject writes; the in-memory selection still works.
  }
}

export function errorMessage(reason: unknown): string {
  if (reason instanceof ApiError) return reason.message;
  if (reason instanceof Error) return reason.message;
  return String(reason);
}

export const useOrganizationsStore = defineStore('organizations', () => {
  const client: HealthClient = createHealthClient();
  const items = ref<Organization[]>([]);
  const activeOrganizationId = ref<string | null>(readStoredActiveOrganization());
  const loading = ref(false);
  const creating = ref(false);
  const error = ref<string | null>(null);
  const loaded = ref(false);

  const activeOrganization = computed<Organization | null>(() =>
    activeOrganizationId.value === null ? null : selected(activeOrganizationId.value),
  );

  function selected(organizationId: string): Organization | null {
    return items.value.find((item) => item.id === organizationId) ?? null;
  }

  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const page = await client.listOrganizations();
      items.value = page.items;
      loaded.value = true;
      if (activeOrganizationId.value !== null && selected(activeOrganizationId.value) === null) {
        setActive(null);
      }
    } catch (cause) {
      error.value = errorMessage(cause);
    } finally {
      loading.value = false;
    }
  }

  async function create(name: string, slug: string): Promise<Organization> {
    creating.value = true;
    error.value = null;
    try {
      const created = await client.createOrganization({ name, slug }, newIdempotencyKey());
      await load();
      setActive(created.id);
      return created;
    } catch (cause) {
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      creating.value = false;
    }
  }

  /** setActive persists only the organization id, never any token or secret. */
  function setActive(id: string | null): void {
    activeOrganizationId.value = id;
    writeStoredActiveOrganization(id);
  }

  return {
    client,
    items,
    activeOrganizationId,
    activeOrganization,
    loading,
    creating,
    error,
    loaded,
    selected,
    load,
    create,
    setActive,
  };
});
