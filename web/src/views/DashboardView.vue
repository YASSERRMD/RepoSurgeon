<script setup lang="ts">
import { computed, onMounted } from 'vue';

import EmptyState from '@/components/EmptyState.vue';
import type { DashboardRun } from '@/api/types';
import { useConsoleStore } from '@/stores/console';

const store = useConsoleStore();

onMounted(() => {
  void store.loadDashboard();
});

const states = computed<Array<[string, number]>>(() =>
  Object.entries(store.runsByState).sort((left, right) => right[1] - left[1]),
);

function stateTone(state: string): string {
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

function runLabel(run: DashboardRun): string {
  return run.project_name === undefined || run.project_name === ''
    ? (run.id ?? '')
    : `${run.project_name} · ${run.state ?? ''}`;
}

const generated = computed<string>(() => {
  const value = store.dashboard?.generated_at;
  return value === undefined || value === '' ? 'not recorded' : new Date(value).toLocaleString();
});

const errorTitle = computed(() => 'Dashboard unavailable.');
</script>

<template>
  <div class="overview">
    <div class="page-head">
      <div>
        <h2 class="page-title">Overview</h2>
        <p class="muted page-subtitle">
          The state of this organization as the server last computed it — runs, issues, blocked work
          and what a repair attempt is waiting on.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="dashboard-reload"
        :disabled="store.loading"
        @click="store.loadDashboard()"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <p v-if="store.error" class="banner banner-error" data-testid="dashboard-error">
      <strong>{{ errorTitle }}</strong>
      {{ store.error }}
      <button
        class="retry"
        type="button"
        data-testid="dashboard-retry"
        @click="store.loadDashboard()"
      >
        Retry
      </button>
    </p>

    <div v-if="store.loading && store.dashboard === null" data-testid="dashboard-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="store.dashboard === null"
      title="No dashboard yet"
      description="The aggregate appears here as soon as the server computes it. An empty overview is the API's answer, never a rendered placeholder."
      test-id="dashboard-empty"
    />

    <template v-else>
      <section class="card metric-grid" data-testid="dashboard-metrics">
        <div class="metric">
          <p class="metric-label">Open issues</p>
          <p class="metric-value" data-testid="metric-open-issues">{{ store.openIssues }}</p>
        </div>
        <div class="metric">
          <p class="metric-label">Runs</p>
          <p class="metric-value" data-testid="metric-total-runs">
            {{ Object.values(store.runsByState).reduce((total, count) => total + count, 0) }}
          </p>
        </div>
        <div class="metric">
          <p class="metric-label">Blocked runs</p>
          <p class="metric-value metric-warn" data-testid="metric-blocked-runs">
            {{ store.blockedRuns }}
          </p>
        </div>
        <div class="metric">
          <p class="metric-label">Failed runs</p>
          <p class="metric-value metric-warn" data-testid="metric-failed-runs">
            {{ store.failedRuns }}
          </p>
        </div>
        <div class="metric">
          <p class="metric-label">Audit events (24h)</p>
          <p class="metric-value" data-testid="metric-audit-events">
            {{ store.recentAuditEvents }}
          </p>
        </div>
        <div class="metric">
          <p class="metric-label">Computed at</p>
          <p class="metric-meta" data-testid="metric-generated">{{ generated }}</p>
        </div>
      </section>

      <section class="card panel" data-testid="dashboard-states">
        <h3 class="card-title">Runs by state</h3>
        <p v-if="states.length === 0" class="muted" data-testid="dashboard-states-none">
          No run has been recorded for this organization yet.
        </p>
        <ul v-else class="state-list">
          <li
            v-for="[state, count] in states"
            :key="state"
            class="state-row"
            :data-testid="`state-${state}`"
          >
            <span class="badge" :class="stateTone(state)" data-testid="state-name">{{
              state
            }}</span>
            <span class="state-count" data-testid="state-count">{{ count }}</span>
          </li>
        </ul>
      </section>

      <section class="card panel" data-testid="dashboard-recent">
        <h3 class="card-title">Recent runs</h3>
        <p v-if="store.recentRuns.length === 0" class="muted" data-testid="dashboard-recent-none">
          Nothing to list yet. Start a run from an issue and it appears here.
        </p>
        <ul v-else class="recent-list">
          <li
            v-for="run in store.recentRuns"
            :key="run.id"
            class="recent-row"
            :data-testid="`recent-${run.id}`"
          >
            <span class="badge" :class="stateTone(run.state ?? '')" data-testid="recent-state">
              {{ run.state ?? 'unknown' }}
            </span>
            <span class="recent-name" data-testid="recent-name">{{ runLabel(run) }}</span>
            <span class="muted" data-testid="recent-attempt">attempt {{ run.attempt ?? 0 }}</span>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.metric-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1rem;
}

.metric-label {
  margin: 0;
  font-size: 0.76rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
}

.metric-value {
  margin: 0.2rem 0 0;
  font-size: 1.6rem;
  font-weight: 700;
  color: var(--color-navy);
}

.metric-warn {
  color: var(--color-gold);
}

.metric-meta {
  margin: 0.2rem 0 0;
  font-size: 0.8rem;
  color: var(--color-text-muted);
}

.state-list,
.recent-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.state-row,
.recent-row {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-surface);
}

.state-count {
  font-weight: 700;
  color: var(--color-navy);
}

.recent-name {
  font-size: 0.86rem;
}
</style>
