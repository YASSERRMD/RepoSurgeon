import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { ApiError, createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import { errorMessage } from '@/stores/organizations';
import type { Project, ProjectCreate, ProjectUpdate } from '@/api/types';

export interface ProjectLoadOptions {
  q?: string;
}

export interface ProjectUpdatePatch extends ProjectUpdate {
  /** version the client last read; a mismatch rejects the whole patch. */
  version: number;
}

export interface ProjectConflict {
  currentVersion: number | null;
  message: string;
}

export const useProjectsStore = defineStore('projects', () => {
  const client: HealthClient = createHealthClient();
  const items = ref<Project[]>([]);
  const scopedOrganizationId = ref<string | null>(null);
  const searchQuery = ref('');
  const nextCursor = ref<string | null>(null);
  const currentId = ref<string | null>(null);
  const loading = ref(false);
  const loadingMore = ref(false);
  const creating = ref(false);
  const saving = ref(false);
  const error = ref<string | null>(null);
  const conflict = ref<ProjectConflict | null>(null);

  const current = computed<Project | null>(
    () => items.value.find((item) => item.id === currentId.value) ?? null,
  );

  function byId(projectId: string): Project | null {
    return items.value.find((item) => item.id === projectId) ?? null;
  }

  function matchesQuery(project: Project, q: string): boolean {
    const needle = q.trim().toLowerCase();
    return needle === '' || project.name.toLowerCase().includes(needle);
  }

  async function load(organizationId: string, options: ProjectLoadOptions = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    conflict.value = null;
    searchQuery.value = options.q ?? '';
    scopedOrganizationId.value = organizationId;
    try {
      const page = await client.listProjects({
        organization_id: organizationId,
        q: searchQuery.value,
      });
      items.value = page.items;
      nextCursor.value = page.next_cursor;
    } catch (cause) {
      items.value = [];
      nextCursor.value = null;
      error.value = errorMessage(cause);
    } finally {
      loading.value = false;
    }
  }

  async function loadMore(): Promise<void> {
    const cursor = nextCursor.value;
    if (cursor === null || loading.value || loadingMore.value) return;
    loadingMore.value = true;
    error.value = null;
    try {
      const page = await client.listProjects({
        organization_id: scopedOrganizationId.value ?? '',
        q: searchQuery.value,
        cursor,
      });
      items.value = [...items.value, ...page.items];
      nextCursor.value = page.next_cursor;
    } catch (cause) {
      error.value = errorMessage(cause);
    } finally {
      loadingMore.value = false;
    }
  }

  async function create(body: ProjectCreate): Promise<Project> {
    creating.value = true;
    error.value = null;
    try {
      const created = await client.createProject(body, newIdempotencyKey());
      scopedOrganizationId.value = created.organization_id;
      if (matchesQuery(created, searchQuery.value)) {
        items.value = [created, ...items.value];
      }
      return created;
    } catch (cause) {
      error.value = errorMessage(cause);
      throw cause;
    } finally {
      creating.value = false;
    }
  }

  async function loadOne(projectId: string): Promise<void> {
    loading.value = true;
    error.value = null;
    conflict.value = null;
    try {
      const project = await client.getProject(projectId);
      currentId.value = project.id;
      const index = items.value.findIndex((item) => item.id === project.id);
      if (index === -1) {
        items.value = [...items.value, project];
      } else {
        items.value = items.value.map((item) => (item.id === project.id ? project : item));
      }
    } catch (cause) {
      currentId.value = null;
      error.value = errorMessage(cause);
    } finally {
      loading.value = false;
    }
  }

  /**
   * update sends the optimistic-concurrency patch. A 409 keeps the server
   * version authoritative: the caller must reload instead of overwriting.
   */
  async function update(projectId: string, patch: ProjectUpdatePatch): Promise<Project> {
    saving.value = true;
    error.value = null;
    conflict.value = null;
    try {
      const updated = await client.updateProject(projectId, patch, newIdempotencyKey());
      items.value = items.value.map((item) => (item.id === updated.id ? updated : item));
      currentId.value = updated.id;
      return updated;
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        conflict.value = {
          currentVersion: cause.currentVersion(),
          message: cause.message,
        };
      } else {
        error.value = errorMessage(cause);
      }
      throw cause;
    } finally {
      saving.value = false;
    }
  }

  function select(projectId: string): void {
    currentId.value = projectId;
  }

  function clearConflict(): void {
    conflict.value = null;
  }

  return {
    client,
    items,
    organizationId: scopedOrganizationId,
    searchQuery,
    nextCursor,
    currentId,
    current,
    loading,
    loadingMore,
    creating,
    saving,
    error,
    conflict,
    byId,
    load,
    loadMore,
    create,
    loadOne,
    update,
    select,
    clearConflict,
  };
});
