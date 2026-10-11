<script setup lang="ts">
import { onMounted } from 'vue';

import EmptyState from '@/components/EmptyState.vue';
import type { ToolDescriptor, ToolRisk } from '@/api/types';
import { useToolsStore } from '@/stores/tools';

const catalog = useToolsStore();

const RISK_LABELS: Record<ToolRisk, string> = {
  read_only: 'read only',
  workspace_write: 'workspace write',
  sandboxed_exec: 'sandboxed exec',
  external_side_effect: 'external side effect',
};

onMounted(() => {
  void catalog.loadTools();
});

function refresh(): void {
  void catalog.loadTools();
}

function permittedLabel(tool: ToolDescriptor): string {
  return tool.permitted ? 'permitted for your role' : 'not permitted for your role';
}
</script>

<template>
  <div class="tools">
    <div class="page-head">
      <div>
        <h2 class="page-title">Tool catalog</h2>
        <p class="muted page-subtitle">
          The complete set of tools a run may request, with the risk class and the permission each
          one needs.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="tools-refresh"
        :disabled="catalog.loadingTools"
        @click="refresh"
      >
        {{ catalog.loadingTools ? 'Refreshing…' : 'Refresh' }}
      </button>
    </div>

    <p class="banner banner-info" data-testid="tools-note">
      <strong>How tools are gated.</strong> Tools outside this catalog do not exist and cannot be
      called: a run may only name a registered tool, and the broker checks the risk class, the
      required permission and the approval flag before anything runs. Model-supplied commands are
      never executed directly — a model output is a request for a catalog tool, which the runner
      carries out inside its sandbox with resource limits and no network by default.
    </p>

    <p v-if="catalog.toolsError" class="banner banner-error" data-testid="tools-error">
      <strong>Could not load the tool catalog.</strong> {{ catalog.toolsError }}
      <button class="retry" type="button" data-testid="tools-retry" @click="refresh">Retry</button>
    </p>

    <div v-if="catalog.loadingTools" data-testid="tools-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="catalog.tools.length === 0"
      title="No tools are registered"
      description="The catalog is empty, so a run cannot request any tool at all."
      test-id="empty-tools"
    />

    <table v-else class="tool-table" data-testid="tools-list">
      <thead>
        <tr>
          <th scope="col">Tool</th>
          <th scope="col">Risk</th>
          <th scope="col">Required permission</th>
          <th scope="col">Your role</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="tool in catalog.tools" :key="tool.name" :data-testid="`tool-${tool.name}`">
          <td>
            <span class="tool-name">{{ tool.name }}</span>
            <span class="tool-description">{{ tool.description }}</span>
            <span class="tool-chips">
              <span
                v-if="tool.approval"
                class="chip chip-approval"
                data-testid="tool-approval-required"
              >
                approval required
              </span>
              <span v-if="tool.sandboxed" class="chip chip-sandbox" data-testid="tool-sandboxed">
                sandboxed
              </span>
            </span>
          </td>
          <td>
            <span class="badge" :class="`risk-${tool.risk}`" data-testid="tool-risk">
              {{ RISK_LABELS[tool.risk] }}
            </span>
          </td>
          <td>
            <code>{{ tool.permission }}</code>
          </td>
          <td>
            <span
              class="permission-flag"
              :class="tool.permitted ? 'permission-allowed' : 'permission-denied'"
              :data-testid="`tool-permitted-${tool.name}`"
            >
              {{ permittedLabel(tool) }}
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.page-title {
  font-size: 1.5rem;
}

.page-subtitle {
  margin: 0.35rem 0 0;
  font-size: 0.9rem;
  max-width: 76ch;
}

.tools {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.banner-info {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
  max-width: 90ch;
}

.banner-error {
  background: var(--color-danger-soft);
  border: 1px solid rgb(180 35 24 / 30%);
  color: var(--color-danger);
}

.retry {
  border: 1px solid currentcolor;
  background: transparent;
  color: inherit;
  padding: 0.25rem 0.7rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.ghost {
  border: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  color: var(--color-navy);
  padding: 0.35rem 0.8rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.ghost:hover:not(:disabled) {
  border-color: var(--color-navy);
}

.tool-table {
  width: 100%;
  border-collapse: collapse;
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  overflow: hidden;
  font-size: 0.86rem;
}

.tool-table th {
  text-align: left;
  padding: 0.6rem 0.9rem;
  background: var(--color-canvas);
  border-bottom: 1px solid var(--color-gray-line);
  font-size: 0.76rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
}

.tool-table td {
  padding: 0.75rem 0.9rem;
  border-bottom: 1px solid var(--color-gray-line);
  vertical-align: top;
}

.tool-table tbody tr:last-child td {
  border-bottom: none;
}

.tool-name {
  display: block;
  font-weight: 650;
  color: var(--color-navy);
}

.tool-description {
  display: block;
  font-size: 0.78rem;
  color: var(--color-text-muted);
  margin-top: 0.15rem;
  max-width: 44ch;
}

.tool-chips {
  display: flex;
  gap: 0.35rem;
  flex-wrap: wrap;
  margin-top: 0.4rem;
}

.chip {
  display: inline-flex;
  align-items: center;
  padding: 0.1rem 0.5rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
  border: 1px solid var(--color-gray-line);
  background: var(--color-canvas);
  color: var(--color-text-muted);
}

.chip-approval {
  background: var(--color-gold-soft);
  border-color: rgb(201 162 39 / 35%);
  color: var(--color-navy);
}

.chip-sandbox {
  background: #eef2ff;
  border-color: #c7d2fe;
  color: #3730a3;
}

.risk-read_only {
  background: #f3f4f6;
  color: var(--color-text-muted);
  border: 1px solid var(--color-gray-line);
}

.risk-workspace_write {
  background: #eef2ff;
  color: #1d4ed8;
  border: 1px solid #bfdbfe;
}

.risk-sandbox_exec {
  background: var(--color-gold-soft);
  color: var(--color-warn);
  border: 1px solid rgb(201 162 39 / 45%);
}

.risk-external_side_effect {
  background: var(--color-danger-soft);
  color: var(--color-danger);
  border: 1px solid rgb(180 35 24 / 30%);
}

.permission-flag {
  font-size: 0.78rem;
  font-weight: 600;
}

.permission-allowed {
  color: var(--color-ok);
}

.permission-denied {
  color: var(--color-text-muted);
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.skeleton {
  height: 4rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
