<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';

import { useCostStore } from '@/stores/cost';

const store = useCostStore();

const selectedModel = ref<string>('');

const modelRows = computed<{ model: string; input_tokens_cached: number; input_tokens_uncached: number; output_tokens: number; usd_cost: number }[]>(() => {
  const rows = store.aggregate?.by_model ?? [];
  if (selectedModel.value === '') return rows;
  return rows.filter((row) => row.model === selectedModel.value);
});

async function load(): Promise<void> {
  await store.load();
}

function usd(value: number): string {
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2 }).format(value);
}

onMounted(load);

watch(
  () => store.aggregate,
  () => {
    // Re-derive selected model rows when data lands.
  },
);
</script>

<template>
  <div class="metrics-view">
    <div class="page-head">
      <div>
        <h2 class="page-title">Metrics</h2>
        <p class="muted page-subtitle">
          Measured token usage and USD cost from the model gateway. Cached and uncached input
          are separated as the provider reports them; nothing is estimated.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="metrics-reload"
        :disabled="store.loading"
        @click="load()"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <div v-if="store.error !== null" class="banner banner-error" data-testid="metrics-error">
      {{ store.error }}
    </div>

    <section class="card panel" data-testid="metrics-panel">
      <div class="metrics-summary">
        <div class="metric">
          <div class="metric-value">{{ usd(store.aggregate?.total_usd ?? 0) }}</div>
          <div class="metric-label">Total cost</div>
        </div>
        <div class="metric">
          <div class="metric-value">{{ store.aggregate?.total_tokens ?? 0 }}</div>
          <div class="metric-label">Total tokens</div>
        </div>
        <div class="metric">
          <div class="metric-value">{{ store.aggregate?.by_model.length ?? 0 }}</div>
          <div class="metric-label">Models used</div>
        </div>
      </div>

      <table class="event-table" aria-label="Usage by model">
        <thead>
          <tr>
            <th>Model</th>
            <th>Input (cached)</th>
            <th>Input (uncached)</th>
            <th>Output</th>
            <th>Cost</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in modelRows" :key="row.model" class="event-row">
            <td class="event-type">{{ row.model }}</td>
            <td>{{ row.input_tokens_cached }}</td>
            <td>{{ row.input_tokens_uncached }}</td>
            <td>{{ row.output_tokens }}</td>
            <td>{{ usd(row.usd_cost) }}</td>
          </tr>
          <tr v-if="modelRows.length === 0">
            <td colspan="5" class="empty-cell">No usage recorded yet.</td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<style scoped>
.metrics-summary {
  display: flex;
  gap: 1rem;
  margin-bottom: 1rem;
}
.metric {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.metric-value {
  font-size: 1.25rem;
  font-weight: 600;
  color: #0a1f44;
}
.metric-label {
  font-size: 0.75rem;
  color: #6b7280;
}
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
