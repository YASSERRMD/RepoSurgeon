<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import AuditEventRow from '@/components/AuditEventRow.vue';
import EmptyState from '@/components/EmptyState.vue';
import { createHealthClient, type HealthClient } from '@/api/client';
import type { AuditEvent } from '@/api/types';

const route = useRoute();
const router = useRouter();
const client: HealthClient = createHealthClient();

const projectId = computed<string>(() => String(route.query.project_id ?? ''));
const cursor = computed<string>(() => String(route.query.cursor ?? ''));
const items = ref<AuditEvent[]>([]);
const nextCursor = ref<string | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);

function queryFor(cursorValue: string, projectValue: string): Record<string, string> {
  const query: Record<string, string> = {};
  if (projectValue !== '') query['project_id'] = projectValue;
  if (cursorValue !== '') query['cursor'] = cursorValue;
  return query;
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const page = await client.listAuditEvents({
      project_id: projectId.value,
      limit: 20,
      ...(cursor.value === '' ? {} : { cursor: cursor.value }),
    });
    items.value = page.items ?? [];
    nextCursor.value = page.next_cursor ?? null;
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
    items.value = [];
    nextCursor.value = null;
  } finally {
    loading.value = false;
  }
}

function applyProject(next: string): void {
  void router.push({ path: '/audit', query: queryFor('', next) });
}

function goNext(): void {
  if (nextCursor.value === null) return;
  void router.push({ path: '/audit', query: queryFor(nextCursor.value, projectId.value) });
}

function createdLabel(value: string | undefined): string {
  return value === undefined || value === '' ? 'not recorded' : new Date(value).toLocaleString();
}

onMounted(load);

watch(
  () => [route.query.project_id, route.query.cursor],
  () => {
    void load();
  },
);
</script>

<template>
  <div class="audit-view">
    <div class="page-head">
      <div>
        <h2 class="page-title">Audit</h2>
        <p class="muted page-subtitle">
          Every recorded action, newest first. The trail is append-only, so an entry here is
          evidence that something happened rather than a status the client derived.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="audit-reload"
        :disabled="loading"
        @click="load()"
      >
        {{ loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <div class="filter-row" role="group" aria-label="Audit filters">
      <input
        :value="projectId"
        type="text"
        class="filter-input"
        aria-label="Project id"
        placeholder="project id (optional)"
        data-testid="audit-project-input"
        @change="applyProject(($event.target as HTMLInputElement).value)"
      />
    </div>

    <p v-if="error" class="banner banner-error" data-testid="audit-error">
      <strong>Could not read the audit trail.</strong>
      {{ error }}
      <button class="retry" type="button" data-testid="audit-retry" @click="load()">Retry</button>
    </p>

    <div v-if="loading && items.length === 0" data-testid="audit-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="items.length === 0"
      title="No audit events yet"
      description="Nothing has been recorded for this scope. An empty trail is the server's answer."
      test-id="audit-empty"
    />

    <ul v-else class="audit-list" data-testid="audit-list">
      <li
        v-for="event in items"
        :key="event.id"
        class="audit-row"
        :data-testid="`audit-${event.id}`"
      >
        <AuditEventRow :event="event" />
        <span class="muted audit-time" data-testid="audit-created">{{
          createdLabel(event.created_at)
        }}</span>
      </li>
    </ul>

    <nav v-if="items.length > 0" class="pager" aria-label="Pagination">
      <button
        class="ghost"
        type="button"
        data-testid="audit-next"
        :disabled="nextCursor === null || loading"
        @click="goNext()"
      >
        Next page
      </button>
      <span class="muted" data-testid="audit-page-state">
        {{ nextCursor === null ? 'no further pages' : 'more pages available' }}
      </span>
    </nav>
  </div>
</template>

<style scoped>
.filter-row {
  display: flex;
  gap: 0.6rem;
  margin-bottom: 1rem;
}

.filter-input {
  padding: 0.4rem 0.6rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  font-size: 0.84rem;
  background: var(--color-surface);
  color: var(--color-navy);
  min-width: 260px;
}

.audit-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.audit-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.5rem 0.65rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-surface);
}

.audit-time {
  font-size: 0.76rem;
  white-space: nowrap;
}

.pager {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 1rem;
}
</style>
