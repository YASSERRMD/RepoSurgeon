<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import { createHealthClient, type HealthClient } from '@/api/client';
import type { RunSummary } from '@/api/types';

const route = useRoute();
const router = useRouter();
const client: HealthClient = createHealthClient();

const projectId = computed<string>(() => String(route.query.project_id ?? ''));
const cursor = computed<string>(() => String(route.query.cursor ?? ''));
const stateFilter = computed<string>(() => String(route.query.state ?? ''));

const items = ref<RunSummary[]>([]);
const nextCursor = ref<string | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);

/** runStates are the state-machine states an operator can filter by. */
const runStates = [
  '',
  'queued',
  'intaking',
  'analyzing',
  'planning',
  'awaiting_approval',
  'editing',
  'testing',
  'reviewing',
  'pr_ready',
  'pr_open',
  'completed',
  'failed',
  'canceled',
  'blocked',
];

function queryFor(cursorValue: string, stateValue: string): Record<string, string> {
  const query: Record<string, string> = {};
  if (projectId.value !== '') query['project_id'] = projectId.value;
  if (stateValue !== '') query['state'] = stateValue;
  if (cursorValue !== '') query['cursor'] = cursorValue;
  return query;
}

async function load(): Promise<void> {
  if (projectId.value === '') {
    items.value = [];
    nextCursor.value = null;
    return;
  }
  loading.value = true;
  error.value = null;
  try {
    const page = await client.listRuns(projectId.value, {
      ...(cursor.value === '' ? {} : { cursor: cursor.value }),
      limit: 20,
      ...(stateFilter.value === '' ? {} : { state: stateFilter.value }),
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

function applyState(next: string): void {
  void router.push({ path: '/runs', query: queryFor('', next) });
}

function applyProject(next: string): void {
  void router.push({
    path: '/runs',
    query: queryFor('', next === '' ? stateFilter.value : stateFilter.value),
  });
}

function goNext(): void {
  if (nextCursor.value === null) return;
  void router.push({ path: '/runs', query: queryFor(nextCursor.value, stateFilter.value) });
}

function goPrevious(): void {
  void router.push({ path: '/runs', query: queryFor('', stateFilter.value) });
}

onMounted(load);

watch(
  () => [route.query.project_id, route.query.state, route.query.cursor],
  () => {
    void load();
  },
);

function stateTone(state: string | undefined): string {
  switch (state) {
    case 'completed':
      return 'badge-ok';
    case 'failed':
    case 'blocked':
      return 'badge-error';
    case 'canceled':
      return 'badge-warning';
    default:
      return 'badge-kind';
  }
}
</script>

<template>
  <div class="runs-list">
    <div class="page-head">
      <div>
        <h2 class="page-title">Runs</h2>
        <p class="muted page-subtitle">
          Every run of the selected project, newest first. The filters above are URL-addressable, so
          a view can be shared or reloaded.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="runs-reload"
        :disabled="loading || projectId === ''"
        @click="load()"
      >
        {{ loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <p v-if="projectId === ''" class="banner" data-testid="runs-no-project">
      Select a project to list its runs. The filter lives in the URL, so the page stays shareable.
    </p>

    <template v-else>
      <div class="filter-row" role="group" aria-label="Run filters">
        <input
          :value="projectId"
          type="text"
          class="filter-input"
          aria-label="Project id"
          data-testid="runs-project-input"
          @change="applyProject(($event.target as HTMLInputElement).value)"
        />
        <select
          :value="stateFilter"
          class="filter-select"
          aria-label="Run state"
          data-testid="runs-state-select"
          @change="applyState(($event.target as HTMLSelectElement).value)"
        >
          <option v-for="state in runStates" :key="state" :value="state">
            {{ state === '' ? 'All states' : state }}
          </option>
        </select>
      </div>

      <p v-if="error" class="banner banner-error" data-testid="runs-error">
        <strong>Could not read the runs.</strong>
        {{ error }}
        <button class="retry" type="button" data-testid="runs-retry" @click="load()">Retry</button>
      </p>

      <div v-if="loading && items.length === 0" data-testid="runs-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="items.length === 0"
        title="No runs for this filter"
        description="The server returned an empty page. That is the answer, not a loading state."
        test-id="runs-empty"
      />

      <table v-else class="grid" data-testid="runs-table">
        <caption class="muted">
          Runs of the selected project{{
            stateFilter === '' ? '' : ` in state ${stateFilter}`
          }}.
        </caption>
        <thead>
          <tr>
            <th scope="col">Run</th>
            <th scope="col">State</th>
            <th scope="col">Attempt</th>
            <th scope="col">Created</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="run in items" :key="run.id" :data-testid="`run-${run.id}`">
            <td>
              <code data-testid="run-id">{{ run.id }}</code>
            </td>
            <td>
              <span class="badge" :class="stateTone(run.state)" data-testid="run-state">
                {{ run.state ?? 'unknown' }}
              </span>
            </td>
            <td data-testid="run-attempt">{{ run.attempt ?? 0 }}</td>
            <td data-testid="run-created">
              {{
                run.created_at === undefined || run.created_at === ''
                  ? 'not recorded'
                  : new Date(run.created_at).toLocaleString()
              }}
            </td>
          </tr>
        </tbody>
      </table>

      <nav v-if="items.length > 0" class="pager" aria-label="Pagination">
        <button
          class="ghost"
          type="button"
          data-testid="runs-prev"
          :disabled="cursor === '' || loading"
          @click="goPrevious()"
        >
          First page
        </button>
        <button
          class="ghost"
          type="button"
          data-testid="runs-next"
          :disabled="nextCursor === null || loading"
          @click="goNext()"
        >
          Next page
        </button>
        <span class="muted" data-testid="runs-page-state">
          {{ cursor === '' ? 'first page' : 'cursor page' }} ·
          {{ nextCursor === null ? 'no further pages' : 'more pages available' }}
        </span>
      </nav>
    </template>
  </div>
</template>

<style scoped>
.filter-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  margin-bottom: 1rem;
}

.filter-input,
.filter-select {
  padding: 0.4rem 0.6rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  font-size: 0.84rem;
  background: var(--color-surface);
  color: var(--color-navy);
}

.filter-input:focus-visible,
.filter-select:focus-visible {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

.grid {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.82rem;
}

.grid caption {
  text-align: left;
  padding-bottom: 0.4rem;
  font-size: 0.78rem;
}

.grid th,
.grid td {
  text-align: left;
  padding: 0.4rem 0.5rem;
  border-bottom: 1px solid var(--color-border);
}

.pager {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 1rem;
}
</style>
