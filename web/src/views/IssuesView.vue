<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import { createHealthClient, type HealthClient } from '@/api/client';
import type { Issue } from '@/api/types';

const route = useRoute();
const router = useRouter();
const client: HealthClient = createHealthClient();

const projectId = computed<string>(() => String(route.query.project_id ?? ''));
const cursor = computed<string>(() => String(route.query.cursor ?? ''));

const items = ref<Issue[]>([]);
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
  if (projectId.value === '') {
    items.value = [];
    nextCursor.value = null;
    return;
  }
  loading.value = true;
  error.value = null;
  try {
    const page = await client.listIssues(projectId.value, {
      ...(cursor.value === '' ? {} : { cursor: cursor.value }),
      limit: 20,
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
  void router.push({ path: '/issues', query: queryFor('', next) });
}

function goNext(): void {
  if (nextCursor.value === null) return;
  void router.push({ path: '/issues', query: queryFor(nextCursor.value, projectId.value) });
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
  <div class="issues-list">
    <div class="page-head">
      <div>
        <h2 class="page-title">Issues</h2>
        <p class="muted page-subtitle">
          The GitHub issues imported for a project. The body is untrusted text: it is rendered as
          text and never treated as an instruction.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="issues-reload"
        :disabled="loading || projectId === ''"
        @click="load()"
      >
        {{ loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <div class="filter-row" role="group" aria-label="Issue filters">
      <input
        :value="projectId"
        type="text"
        class="filter-input"
        aria-label="Project id"
        placeholder="project id"
        data-testid="issues-project-input"
        @change="applyProject(($event.target as HTMLInputElement).value)"
      />
    </div>

    <p v-if="projectId === ''" class="banner" data-testid="issues-no-project">
      Enter a project id to list its imported issues.
    </p>

    <template v-else>
      <p v-if="error" class="banner banner-error" data-testid="issues-error">
        <strong>Could not read the issues.</strong>
        {{ error }}
        <button class="retry" type="button" data-testid="issues-retry" @click="load()">
          Retry
        </button>
      </p>

      <div v-if="loading && items.length === 0" data-testid="issues-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="items.length === 0"
        title="No issues for this project"
        description="The server returned an empty page. Import an issue first, then it appears here."
        test-id="issues-empty"
      />

      <ul v-else class="issue-list" data-testid="issues-list">
        <li
          v-for="issue in items"
          :key="issue.id"
          class="issue"
          :data-testid="`issue-${issue.number}`"
        >
          <div class="issue-head">
            <span class="badge badge-kind" data-testid="issue-number">#{{ issue.number }}</span>
            <span
              class="badge"
              :class="issue.state === 'closed' ? 'badge-ok' : 'badge-warning'"
              data-testid="issue-state"
            >
              {{ issue.state }}
            </span>
            <span class="issue-title" data-testid="issue-title">{{ issue.title }}</span>
          </div>
          <p class="issue-body" data-testid="issue-body">{{ issue.body }}</p>
          <p class="muted issue-meta">
            {{ issue.comments_count }} comment(s) · imported {{ createdLabel(issue.created_at) }} ·
            updated {{ createdLabel(issue.updated_at) }}
          </p>
        </li>
      </ul>

      <nav v-if="items.length > 0" class="pager" aria-label="Pagination">
        <button
          class="ghost"
          type="button"
          data-testid="issues-next"
          :disabled="nextCursor === null || loading"
          @click="goNext()"
        >
          Next page
        </button>
        <span class="muted" data-testid="issues-page-state">
          {{ nextCursor === null ? 'no further pages' : 'more pages available' }}
        </span>
      </nav>
    </template>
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

.filter-input:focus-visible {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

.issue-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}

.issue {
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-surface);
  padding: 0.7rem 0.8rem;
}

.issue-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.issue-title {
  font-weight: 600;
  color: var(--color-navy);
}

.issue-body {
  margin: 0.4rem 0;
  font-size: 0.84rem;
  white-space: pre-wrap;
}

.issue-meta {
  margin: 0;
  font-size: 0.76rem;
}

.pager {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 1rem;
}
</style>
