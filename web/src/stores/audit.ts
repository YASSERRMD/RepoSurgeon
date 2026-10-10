import { ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, type HealthClient } from '@/api/client';
import { errorMessage } from '@/stores/organizations';
import type { AuditEvent, AuditEventPage } from '@/api/types';

export const useAuditStore = defineStore('audit', () => {
  const client: HealthClient = createHealthClient();
  const items = ref<AuditEvent[]>([]);
  const scopedProjectId = ref<string | null>(null);
  const filterAction = ref('');
  const appliedAction = ref('');
  const nextCursor = ref<string | null>(null);
  const loading = ref(false);
  const loadingMore = ref(false);
  const error = ref<string | null>(null);
  const loadCount = ref(0);

  async function load(projectId: string, action = ''): Promise<void> {
    loading.value = true;
    error.value = null;
    scopedProjectId.value = projectId;
    appliedAction.value = action;
    try {
      const page = await client.listAuditEvents({ project_id: projectId, action });
      applyPage(page);
      loadCount.value += 1;
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
    const project = scopedProjectId.value;
    if (cursor === null || project === null || loading.value || loadingMore.value) return;
    loadingMore.value = true;
    error.value = null;
    try {
      const page = await client.listAuditEvents({
        project_id: project,
        action: appliedAction.value,
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

  function setFilterAction(action: string): void {
    filterAction.value = action;
  }

  function applyPage(page: AuditEventPage): void {
    items.value = page.items;
    nextCursor.value = page.next_cursor;
  }

  return {
    client,
    items,
    projectId: scopedProjectId,
    filterAction,
    appliedAction,
    nextCursor,
    loading,
    loadingMore,
    error,
    loadCount,
    load,
    loadMore,
    setFilterAction,
  };
});
