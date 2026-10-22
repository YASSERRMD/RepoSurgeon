<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { createHealthClient, type HealthClient } from '@/api/client';
import type { RunEvent } from '@/api/types';

const route = useRoute();
const router = useRouter();
const client: HealthClient = createHealthClient();

const cursor = computed<string>(() => String(route.query.cursor ?? ''));
const items = ref<RunEvent[]>([]);
const nextCursor = ref<string | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);

async function load(after?: string): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const page = await client.listOrgEvents(after ? { after } : {});
    items.value = page.items ?? [];
    nextCursor.value = page.next_cursor ?? null;
  } catch (cause) {
    items.value = [];
    nextCursor.value = null;
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}

function goNext(): void {
  if (nextCursor.value === null) return;
  void router.push({ path: '/timeline', query: { cursor: nextCursor.value } });
}

function createdLabel(value: string | undefined): string {
  return value === undefined || value === '' ? 'not recorded' : new Date(value).toLocaleString();
}

onMounted(() => void load(cursor.value));

watch(
  () => route.query.cursor,
  () => {
    void load(cursor.value);
  },
);
</script>

<template>
  <div class="timeline-view">
    <div class="page-head">
      <div>
        <h2 class="page-title">Timeline</h2>
        <p class="muted page-subtitle">
          The append-only run event log for this organization. Every state change, step, tool call,
          test result, approval, pull-request action and recovery is recorded here before it is
          streamed to the timeline.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="timeline-reload"
        :disabled="loading"
        @click="load()"
      >
        {{ loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <div v-if="error !== null" class="banner banner-error" data-testid="timeline-error">
      {{ error }}
    </div>

    <section class="card panel" data-testid="timeline-panel">
      <table class="event-table" aria-label="Run events">
        <thead>
          <tr>
            <th>When</th>
            <th>Type</th>
            <th>Step</th>
            <th>Event</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="event in items" :key="event.id" class="event-row">
            <td class="event-time" data-testid="event-time">
              {{ createdLabel(event.created_at) }}
            </td>
            <td class="event-type" data-testid="event-type">{{ event.type }}</td>
            <td class="event-step" data-testid="event-step">{{ event.step ?? '—' }}</td>
            <td class="event-payload" data-testid="event-payload">
              {{ JSON.stringify(event.payload) }}
            </td>
          </tr>
          <tr v-if="items.length === 0">
            <td colspan="4" class="empty-cell">No events recorded yet.</td>
          </tr>
        </tbody>
      </table>

      <div v-if="nextCursor !== null" class="action-row">
        <button
          class="primary"
          type="button"
          data-testid="timeline-next"
          :disabled="loading"
          @click="goNext()"
        >
          Load more
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.event-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}
.event-table th {
  text-align: left;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid #d1d5db;
  font-weight: 600;
}
.event-row td {
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid #e5e7eb;
  vertical-align: top;
}
.event-row:hover {
  background: #f9fafb;
}
.event-time {
  white-space: nowrap;
}
.event-type {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.event-payload {
  max-width: 40rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.empty-cell {
  text-align: center;
  color: #6b7280;
  padding: 1.5rem;
}
.action-row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 0.75rem;
}
</style>
