<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type { ContextItem, ContextManifest, ContextTokenBudget, ContextRefresh } from '@/api/types';
import { useAuthStore } from '@/stores/auth';
import { useContextStore } from '@/stores/context';

const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const store = useContextStore();

const runId = computed(() => String(route.params.runId ?? ''));

const runInput = ref('');
const rebuildQuery = ref('');
const rebuildWorkspaceId = ref('');
const rebuildBudget = ref<ContextTokenBudget>(32768);
const rebuildExpand = ref(false);

const canRebuild = computed(() => auth.can('run.execute'));

watch(
  runId,
  (next) => {
    store.bindRun(next);
    if (next !== '') void store.loadManifest();
  },
  { immediate: true },
);

const manifest = computed<ContextManifest | null>(() => store.manifest);

const usagePercent = computed(() => {
  const current = manifest.value;
  if (current === null || current.token_budget <= 0) return 0;
  const percent = Math.round((current.token_estimate / current.token_budget) * 100);
  return Math.min(percent, 100);
});

const usageLabel = computed(() => {
  const current = manifest.value;
  if (current === null) return '';
  return `${current.token_estimate.toLocaleString()} of ${current.token_budget.toLocaleString()} tokens`;
});

/** Notes and omissions come from the manifest; the UI never adds to either list. */
const untrustedNotes = computed<string[]>(() => manifest.value?.untrusted_notes ?? []);
const omittedPaths = computed<string[]>(() => manifest.value?.omitted_paths ?? []);

/**
 * A failure while a manifest is on screen is a refused rebuild, not a missing
 * manifest, and the banner says so with the code the server returned.
 */
const errorTitle = computed(() =>
  manifest.value === null ? 'Context unavailable.' : 'Context refresh failed.',
);

/** The budget choices the contract enumerates; the full window is never used. */
const TOKEN_BUDGETS: ContextTokenBudget[] = [32768, 131072, 262144];

interface NumberedLine {
  number: number;
  text: string;
}

/** numberedLines keeps the manifest's own line range on every rendered line. */
function numberedLines(item: ContextItem): NumberedLine[] {
  return item.content.split('\n').map((text, index) => ({
    number: item.line_start + index,
    text,
  }));
}

/** shortHash keeps a digest comparable without printing the whole thing. */
function shortHash(value: string): string {
  return value.length <= 16 ? value : `${value.slice(0, 16)}…`;
}

function rangeLabel(start: number, end: number): string {
  return start === end ? `line ${start}` : `lines ${start}–${end}`;
}

function loadManifest(): void {
  void store.loadManifest();
}

/** The workspace id is part of the contract: a rebuild always names the index. */
function prefillRebuild(current: ContextManifest): void {
  rebuildWorkspaceId.value = current.workspace_id;
  if (rebuildQuery.value.trim() === '') rebuildQuery.value = current.query;
}

watch(
  manifest,
  (current) => {
    if (current !== null) prefillRebuild(current);
  },
  { immediate: true },
);

function submitRebuild(): void {
  const query = rebuildQuery.value.trim();
  const workspaceId = rebuildWorkspaceId.value.trim();
  if (query === '' || workspaceId === '') return;
  const body: ContextRefresh = {
    workspace_id: workspaceId,
    query,
    token_budget: rebuildBudget.value,
    expand: rebuildExpand.value,
  };
  void store.refresh(body).catch(() => {
    // store.error already carries the server's code and message; the refusal
    // is reported in the banner instead of an unhandled rejection.
  });
}

function openRun(): void {
  const id = runInput.value.trim();
  if (id === '') return;
  void router.push(`/runs/${encodeURIComponent(id)}/context`);
}
</script>

<template>
  <div class="context-inspector">
    <div class="page-head">
      <div>
        <h2 class="page-title">Context inspector</h2>
        <p class="muted page-subtitle">
          The manifest a run actually used: what was retrieved, why, and how much of the token
          budget it consumed.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="context-reload"
        :disabled="store.loading"
        @click="loadManifest"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <section v-if="runId === ''" class="card prompt" data-testid="context-prompt">
      <p>
        <strong>No run selected.</strong> Paste a run identifier to inspect the context it used.
      </p>
      <form class="prompt-form" data-testid="run-form" @submit.prevent="openRun">
        <input
          v-model="runInput"
          type="text"
          placeholder="run_1a2b3c4d5e6f7g8h9i0j"
          aria-label="Run identifier"
          data-testid="run-input"
        />
        <button class="primary" type="submit" data-testid="run-open">Open</button>
      </form>
    </section>

    <template v-else>
      <p v-if="store.error" class="banner banner-error" data-testid="context-error">
        <strong>{{ errorTitle }}</strong>
        <span data-testid="context-error-code">{{ store.error.code }}</span>
        — {{ store.error.message }}
        <button class="retry" type="button" data-testid="context-retry" @click="loadManifest">
          Retry
        </button>
      </p>

      <div v-if="store.loading" data-testid="context-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="manifest === null"
        title="No context manifest yet"
        description="A manifest appears here as soon as a run builds its context. Until then there is nothing to report for this run."
        test-id="context-empty"
      />

      <template v-else>
        <section class="card panel" data-testid="context-summary">
          <header class="panel-head">
            <h3 class="card-title">Manifest</h3>
            <span class="badge badge-strategy" data-testid="context-strategy">{{
              manifest.strategy
            }}</span>
            <span
              class="badge"
              :class="manifest.truncated ? 'badge-truncated' : 'badge-complete'"
              data-testid="context-truncated"
            >
              {{ manifest.truncated ? 'truncated' : 'complete' }}
            </span>
          </header>

          <p class="muted summary-line" data-testid="context-usage">
            {{ usageLabel }} · {{ usagePercent }}% of the budget
          </p>
          <div
            class="budget-bar"
            role="progressbar"
            :aria-valuenow="usagePercent"
            aria-valuemin="0"
            aria-valuemax="100"
            data-testid="context-budget-bar"
          >
            <span class="budget-fill" :style="{ width: `${usagePercent}%` }" />
          </div>

          <dl class="facts">
            <div>
              <dt>Run</dt>
              <dd data-testid="context-run-id">{{ manifest.run_id ?? runId }}</dd>
            </div>
            <div>
              <dt>Workspace</dt>
              <dd data-testid="context-workspace-id">{{ manifest.workspace_id }}</dd>
            </div>
            <div>
              <dt>Query</dt>
              <dd data-testid="context-query">{{ manifest.query }}</dd>
            </div>
            <div>
              <dt>Token budget</dt>
              <dd data-testid="context-token-budget">
                {{ manifest.token_budget.toLocaleString() }}
              </dd>
            </div>
            <div>
              <dt>Token estimate</dt>
              <dd data-testid="context-token-estimate">
                {{ manifest.token_estimate.toLocaleString() }}
              </dd>
            </div>
            <div>
              <dt>Built</dt>
              <dd data-testid="context-created-at">
                {{ new Date(manifest.created_at).toLocaleString() }}
              </dd>
            </div>
          </dl>

          <div class="omitted" data-testid="context-omitted">
            <h4 class="omitted-title">Omitted paths</h4>
            <p v-if="omittedPaths.length === 0" class="muted">
              Nothing was left out of this manifest.
            </p>
            <ul v-else class="omitted-list">
              <li v-for="path in omittedPaths" :key="path" :data-testid="`omitted-${path}`">
                <code>{{ path }}</code>
              </li>
            </ul>
          </div>
        </section>

        <section
          v-if="untrustedNotes.length > 0"
          class="card panel"
          data-testid="context-untrusted-notes"
        >
          <h3 class="card-title">Untrusted content notes</h3>
          <ul class="notes-list">
            <li v-for="note in untrustedNotes" :key="note" :data-testid="`context-note`">
              {{ note }}
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="context-items">
          <h3 class="card-title">Retrieved ranges ({{ manifest.items.length }})</h3>

          <EmptyState
            v-if="manifest.items.length === 0"
            title="This manifest retrieved nothing"
            description="The run built a context with no ranges. The empty list is the server's answer, not a loading state."
            test-id="context-items-empty"
          />

          <ol v-else class="item-list">
            <li
              v-for="item in manifest.items"
              :key="`${item.path}:${item.line_start}-${item.line_end}`"
              class="item"
              :data-testid="`item-${item.path}-${item.line_start}`"
            >
              <div class="item-head">
                <span class="item-path" data-testid="item-path">{{ item.path }}</span>
                <span class="badge badge-lines" data-testid="item-range">{{
                  rangeLabel(item.line_start, item.line_end)
                }}</span>
                <span class="badge badge-tokens" data-testid="item-tokens">{{
                  item.token_estimate.toLocaleString()
                }}</span>
              </div>
              <p class="item-reason" data-testid="item-reason">{{ item.reason }}</p>
              <p class="item-sha" data-testid="item-sha">
                sha256 <code :title="item.sha256">{{ shortHash(item.sha256) }}</code>
              </p>
              <div class="code" data-testid="item-content">
                <pre
                  v-for="line in numberedLines(item)"
                  :key="line.number"
                  class="code-line"
                  :data-testid="`item-line-${line.number}`"
                ><span class="line-number">{{ line.number }}</span
                  ><span class="line-text">{{ line.text }}</span></pre>
              </div>
            </li>
          </ol>
        </section>

        <section class="card panel" data-testid="rebuild-panel">
          <h3 class="card-title">Rebuild context</h3>

          <p v-if="!canRebuild" class="banner banner-info" data-testid="rebuild-no-permission">
            <strong>Rebuilding is restricted.</strong> Only a role that carries the
            <code>run.execute</code> permission in the active organization can rebuild a run's
            context. The manifest above still shows what the run used, and the API refuses a rebuild
            sent from an account without that permission.
          </p>

          <form
            v-else
            class="rebuild-form"
            data-testid="rebuild-form"
            novalidate
            @submit.prevent="submitRebuild"
          >
            <FormField
              label="Workspace id"
              field-id="rebuild-workspace"
              hint="The index the rebuild reads. Taken from the manifest when one is loaded."
            >
              <input
                id="rebuild-workspace"
                v-model="rebuildWorkspaceId"
                type="text"
                placeholder="wsp_1a2b3c4d5e6f7g8h9i0j"
                data-testid="rebuild-workspace-input"
              />
            </FormField>
            <FormField
              label="Query"
              field-id="rebuild-query"
              hint="Search terms used to retrieve the context ranges."
            >
              <input
                id="rebuild-query"
                v-model="rebuildQuery"
                type="text"
                placeholder="pagination off by one in list handler"
                data-testid="rebuild-query-input"
              />
            </FormField>
            <FormField
              label="Token budget"
              field-id="rebuild-budget"
              hint="The server defaults to 32768 tokens; the full model window is never the default."
            >
              <select
                id="rebuild-budget"
                v-model="rebuildBudget"
                data-testid="rebuild-budget-select"
              >
                <option v-for="budget in TOKEN_BUDGETS" :key="budget" :value="budget">
                  {{ budget.toLocaleString() }} tokens
                </option>
              </select>
            </FormField>
            <label class="expand">
              <input v-model="rebuildExpand" type="checkbox" data-testid="rebuild-expand" />
              <span>Expand the manifest with additional retrieval after the first pass</span>
            </label>
            <div class="rebuild-actions">
              <button
                class="primary"
                type="submit"
                data-testid="rebuild-submit"
                :disabled="
                  store.loading || rebuildQuery.trim() === '' || rebuildWorkspaceId.trim() === ''
                "
              >
                {{ store.loading ? 'Rebuilding…' : 'Rebuild context' }}
              </button>
              <span class="muted">The response replaces the manifest above; nothing is local.</span>
            </div>
          </form>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.context-inspector {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

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

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  padding: 1.25rem;
}

.panel {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.card-title {
  font-size: 1rem;
}

.prompt {
  font-size: 0.9rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.prompt-form {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.prompt-form input {
  flex: 1 1 22rem;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.banner-error {
  background: var(--color-danger-soft);
  border: 1px solid rgb(180 35 24 / 30%);
  color: var(--color-danger);
}

.banner-info {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
  max-width: 90ch;
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

.primary {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.5rem 1rem;
  border-radius: var(--radius-sm);
  font-weight: 600;
}

.primary:hover:not(:disabled) {
  background: var(--color-navy-soft);
}

.summary-line {
  margin: 0;
  font-size: 0.85rem;
}

.budget-bar {
  height: 0.7rem;
  border-radius: 999px;
  border: 1px solid var(--color-gray-line);
  background: var(--color-canvas);
  overflow: hidden;
}

.budget-fill {
  display: block;
  height: 100%;
  background: var(--color-gold);
}

.facts {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: 0.6rem 1.25rem;
}

.facts dt {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
}

.facts dd {
  margin: 0.1rem 0 0;
  font-size: 0.85rem;
  word-break: break-all;
}

.badge {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.15rem 0.55rem;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
}

.badge-strategy {
  background: var(--color-navy);
  color: #fff;
  border: 1px solid var(--color-navy);
}

.badge-truncated {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 45%);
}

.badge-complete {
  background: var(--color-ok-soft);
  color: var(--color-ok);
  border: 1px solid rgb(28 124 70 / 30%);
}

.omitted-title {
  margin: 0 0 0.35rem;
  font-size: 0.82rem;
  color: var(--color-navy);
}

.omitted-list,
.notes-list {
  margin: 0;
  padding-left: 1.1rem;
  font-size: 0.82rem;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.item-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}

.item {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  padding: 0.75rem 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.item-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.item-path {
  font-weight: 650;
  color: var(--color-navy);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
}

.badge-lines {
  background: var(--color-canvas);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
}

.badge-tokens {
  background: #eef2ff;
  border: 1px solid #c7d2fe;
  color: #3730a3;
}

.item-reason,
.item-sha {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.code {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-canvas);
  overflow-x: auto;
}

.code-line {
  margin: 0;
  display: flex;
  gap: 0.75rem;
  padding: 0 0.6rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.55;
}

.line-number {
  flex: 0 0 3.5rem;
  text-align: right;
  color: var(--color-text-muted);
  user-select: none;
}

.line-text {
  white-space: pre-wrap;
  word-break: break-word;
}

.rebuild-form {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  max-width: 40rem;
}

.rebuild-form input,
.rebuild-form select {
  width: 100%;
}

.expand {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.82rem;
}

.expand input {
  width: auto;
}

.rebuild-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
  font-size: 0.8rem;
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.skeleton {
  height: 6rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
