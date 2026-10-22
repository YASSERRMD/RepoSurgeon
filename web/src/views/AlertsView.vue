<script setup lang="ts">
import { onMounted, watch } from 'vue';

import { useAlertsStore } from '@/stores/alerts';

const store = useAlertsStore();

const tone = (severity: string): string => {
  switch (severity) {
    case 'critical':
      return 'badge-error';
    case 'warn':
      return 'badge-pending';
    default:
      return 'badge-ok';
  }
};

async function load(): Promise<void> {
  await store.load();
}

onMounted(load);

watch(
  () => store.items,
  () => {
    // Re-render when alerts change.
  },
);
</script>

<template>
  <div class="alerts-view">
    <div class="page-head">
      <div>
        <h2 class="page-title">Alerts</h2>
        <p class="muted page-subtitle">
          Evaluated alert conditions for this organization: runaway cost, stuck leases,
          growing queue age, failed CI, model rate-limit pressure and unexpected secret
          denials.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="alerts-reload"
        :disabled="store.loading"
        @click="load()"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <div v-if="store.error !== null" class="banner banner-error" data-testid="alerts-error">
      {{ store.error }}
    </div>

    <section class="card panel" data-testid="alerts-panel">
      <table class="event-table" aria-label="Active alerts">
        <thead>
          <tr>
            <th>Severity</th>
            <th>Condition</th>
            <th>Message</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="alert in store.items" :key="alert.condition + alert.message" class="event-row">
            <td class="event-type">
              <span class="badge" :class="tone(alert.severity)">{{ alert.severity }}</span>
            </td>
            <td>{{ alert.condition }}</td>
            <td>{{ alert.message }}</td>
          </tr>
          <tr v-if="store.items.length === 0">
            <td colspan="3" class="empty-cell">No active alerts.</td>
          </tr>
        </tbody>
      </table>
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
.event-type {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.empty-cell {
  text-align: center;
  color: #6b7280;
  padding: 1.5rem;
}
</style>
