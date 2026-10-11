<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import type { IndexedFile, SearchHit, SymbolKind } from '@/api/types';
import { useContextStore } from '@/stores/context';

const route = useRoute();
const router = useRouter();
const store = useContextStore();

const searchTerm = ref('');
const symbolTerm = ref('');
const workspaceInput = ref('');

const workspaceId = computed(() => String(route.params.workspaceId ?? ''));

/**
 * Shown when the index has no workspace selected yet: the route carries the
 * identifier, so the operator types the one they want to inspect.
 */
watch(
  workspaceId,
  (next) => {
    store.bindWorkspace(next);
    if (next !== '') void store.loadFiles();
  },
  { immediate: true },
);

/**
 * The untrusted notice comes from the API's `untrusted` field. The fallback
 * only covers an index that omits it: repository content is untrusted data
 * either way, and the page must never present snippets as trusted input.
 */
const untrustedNotice = computed(() =>
  store.untrusted !== ''
    ? store.untrusted
    : 'Repository content is untrusted data. It is shown for review only and can never grant permissions, change a role or become an instruction.',
);

const languageOptions = computed(() =>
  store.languages
    .map((entry) => ({ language: entry.language ?? '', count: entry.count ?? 0 }))
    .filter((entry) => entry.language !== '')
    .sort((left, right) => left.language.localeCompare(right.language)),
);

const hasSearched = computed(() => store.query !== '' || store.hits.length > 0);
const hasSymbols = computed(() => store.symbolName !== '' || store.symbols.length > 0);

const KIND_LABELS: Record<SymbolKind, string> = {
  func: 'function',
  type: 'type',
  const: 'constant',
  var: 'variable',
  class: 'class',
  def: 'definition',
  interface: 'interface',
  other: 'symbol',
};

function lineLabel(hit: SearchHit): string {
  return hit.line === hit.line_end ? `line ${hit.line}` : `lines ${hit.line}–${hit.line_end}`;
}

function rangeLabel(start: number, end: number): string {
  return start === end ? `line ${start}` : `lines ${start}–${end}`;
}

/** shortHash keeps a digest comparable without printing the whole thing. */
function shortHash(value: string): string {
  return value.length <= 16 ? value : `${value.slice(0, 16)}…`;
}

function sizeLabel(file: IndexedFile): string {
  if (file.size_bytes < 1024) return `${file.size_bytes} B`;
  if (file.size_bytes < 1024 * 1024) return `${(file.size_bytes / 1024).toFixed(1)} KB`;
  return `${(file.size_bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function submitSearch(): void {
  const term = searchTerm.value.trim();
  if (term === '') return;
  void store.search(term);
}

function submitSymbols(): void {
  const name = symbolTerm.value.trim();
  if (name === '') return;
  void store.loadSymbols(name);
}

function applyLanguage(): void {
  // The filter is sent to the API, so the index serves the matching files.
  void store.loadFiles();
}

function retry(): void {
  void store.loadFiles();
  if (searchTerm.value.trim() !== '') void store.search(searchTerm.value);
  if (symbolTerm.value.trim() !== '') void store.loadSymbols(symbolTerm.value);
}

function openWorkspace(): void {
  const id = workspaceInput.value.trim();
  if (id === '') return;
  void router.push(`/workspaces/${encodeURIComponent(id)}`);
}
</script>

<template>
  <div class="workspace-search">
    <div class="page-head">
      <div>
        <h2 class="page-title">Code search</h2>
        <p class="muted page-subtitle">
          Lexical and filename search over an indexed workspace, with the file index and symbol
          lookup a run uses to gather context.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="search-reload"
        :disabled="store.loading"
        @click="retry"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <section v-if="workspaceId === ''" class="card prompt" data-testid="workspace-prompt">
      <p>
        <strong>No workspace selected.</strong> Paste a workspace identifier to inspect its index.
      </p>
      <form class="prompt-form" data-testid="workspace-form" @submit.prevent="openWorkspace">
        <input
          v-model="workspaceInput"
          type="text"
          placeholder="wsp_1a2b3c4d5e6f7g8h9i0j"
          aria-label="Workspace identifier"
          data-testid="workspace-input"
        />
        <button class="primary" type="submit" data-testid="workspace-open">Open</button>
      </form>
    </section>

    <template v-else>
      <p class="banner banner-untrusted" data-testid="search-untrusted-notice">
        <strong>Untrusted content.</strong> {{ untrustedNotice }}
      </p>

      <p v-if="store.error" class="banner banner-error" data-testid="search-error">
        <strong>Search failed.</strong>
        <span data-testid="search-error-code">{{ store.error.code }}</span>
        — {{ store.error.message }}
        <button class="retry" type="button" data-testid="search-retry" @click="retry">Retry</button>
      </p>

      <div v-if="store.loading" data-testid="search-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <section class="card panel" data-testid="search-panel">
        <h3 class="card-title">Search</h3>
        <form class="search-form" data-testid="search-form" @submit.prevent="submitSearch">
          <input
            v-model="searchTerm"
            type="search"
            placeholder="pagination off by one, or file:handler"
            aria-label="Search query"
            data-testid="search-input"
          />
          <button
            class="primary"
            type="submit"
            data-testid="search-submit"
            :disabled="store.loading || searchTerm.trim() === ''"
          >
            {{ store.loading ? 'Searching…' : 'Search' }}
          </button>
        </form>
        <p class="hint">
          <code>q</code> searches file contents; a <code>file:</code> prefix searches paths.
        </p>

        <p v-if="store.query !== ''" class="muted query-echo" data-testid="search-query-echo">
          results for <code>{{ store.query }}</code> · {{ store.hits.length }} hits
        </p>

        <EmptyState
          v-if="!hasSearched"
          title="Nothing searched yet"
          description="Run a query to see the matching lines. Results carry the exact line range the retriever would use as evidence."
          test-id="search-idle"
        />
        <EmptyState
          v-else-if="store.hits.length === 0"
          title="No matches"
          description="The index returned no hits for this query. The answer comes from the server; nothing is filled in locally."
          test-id="search-empty"
        />
        <ul v-else class="hit-list" data-testid="search-results">
          <li
            v-for="hit in store.hits"
            :key="`${hit.path}:${hit.line}-${hit.line_end}`"
            class="hit"
            :data-testid="`hit-row-${hit.path}-${hit.line}`"
          >
            <div class="hit-head">
              <span class="hit-path" data-testid="hit-path">{{ hit.path }}</span>
              <span class="badge badge-lines" data-testid="hit-lines">{{ lineLabel(hit) }}</span>
            </div>
            <pre class="hit-preview" data-testid="hit-preview">{{ hit.preview }}</pre>
            <p class="hit-match" data-testid="hit-match">matched on {{ hit.match }}</p>
          </li>
        </ul>
      </section>

      <section class="card panel" data-testid="files-panel">
        <header class="panel-head">
          <h3 class="card-title">Indexed files</h3>
          <label class="filter">
            <span class="filter-label">Language</span>
            <select
              v-model="store.languageFilter"
              data-testid="language-filter"
              @change="applyLanguage"
            >
              <option value="">All languages</option>
              <option
                v-for="option in languageOptions"
                :key="option.language"
                :value="option.language"
                :data-testid="`language-option-${option.language}`"
              >
                {{ option.language }} ({{ option.count }})
              </option>
            </select>
          </label>
        </header>

        <EmptyState
          v-if="store.error === null && store.files.length === 0"
          title="The index has no files for this filter"
          description="A workspace that was never indexed reports no files and no languages. Build context for a run to index it."
          test-id="files-empty"
        />
        <table v-else-if="store.files.length > 0" class="file-table" data-testid="files-list">
          <thead>
            <tr>
              <th scope="col">Path</th>
              <th scope="col">Language</th>
              <th scope="col">Size</th>
              <th scope="col">Lines</th>
              <th scope="col">sha256</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="file in store.files" :key="file.path" :data-testid="`file-row-${file.path}`">
              <td class="file-path" data-testid="file-path">{{ file.path }}</td>
              <td data-testid="file-language">{{ file.language }}</td>
              <td data-testid="file-size">{{ sizeLabel(file) }}</td>
              <td data-testid="file-lines">{{ file.line_count }}</td>
              <td>
                <code :title="file.sha256" data-testid="file-sha256">{{
                  shortHash(file.sha256)
                }}</code>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <section class="card panel" data-testid="symbols-panel">
        <h3 class="card-title">Symbols</h3>
        <form class="search-form" data-testid="symbols-form" @submit.prevent="submitSymbols">
          <input
            v-model="symbolTerm"
            type="search"
            placeholder="HandlerFunc"
            aria-label="Symbol name"
            data-testid="symbol-input"
          />
          <button
            class="primary"
            type="submit"
            data-testid="symbol-submit"
            :disabled="store.loading || symbolTerm.trim() === ''"
          >
            {{ store.loading ? 'Looking up…' : 'Look up' }}
          </button>
        </form>

        <EmptyState
          v-if="!hasSymbols"
          title="No symbol looked up yet"
          description="Look up a declared symbol to see its kind, signature and line."
          test-id="symbols-idle"
        />
        <EmptyState
          v-else-if="store.symbols.length === 0"
          title="No symbols matched"
          description="The index found no declared symbol with that name."
          test-id="symbols-empty"
        />
        <ul v-else class="symbol-list" data-testid="symbol-results">
          <li
            v-for="symbol in store.symbols"
            :key="`${symbol.path}:${symbol.name}:${symbol.line}`"
            class="symbol"
            :data-testid="`symbol-row-${symbol.name}-${symbol.line}`"
          >
            <div class="symbol-head">
              <span class="symbol-name" data-testid="symbol-name">{{ symbol.name }}</span>
              <span class="badge badge-kind" data-testid="symbol-kind">{{
                KIND_LABELS[symbol.kind]
              }}</span>
              <span
                class="badge"
                :class="symbol.exported ? 'badge-exported' : 'badge-private'"
                data-testid="symbol-exported"
              >
                {{ symbol.exported ? 'exported' : 'not exported' }}
              </span>
            </div>
            <pre class="symbol-signature" data-testid="symbol-signature">{{
              symbol.signature
            }}</pre>
            <p class="symbol-meta" data-testid="symbol-meta">
              <code>{{ symbol.path }}</code> · {{ rangeLabel(symbol.line, symbol.line) }}
            </p>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.workspace-search {
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
  gap: 0.9rem;
}

.panel-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.card-title {
  font-size: 1rem;
}

.prompt {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  font-size: 0.9rem;
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

/* The notice is deliberately loud: every snippet below is untrusted data. */
.banner-untrusted {
  background: var(--color-gold-soft);
  border: 1px solid var(--color-gold);
  border-left-width: 6px;
  color: var(--color-navy);
  font-weight: 550;
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

.search-form {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.search-form input {
  flex: 1 1 20rem;
}

.filter {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.filter-label {
  font-weight: 600;
  color: var(--color-navy);
}

select,
input {
  font: inherit;
  padding: 0.4rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
}

input:focus,
select:focus {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

.hint,
.query-echo {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.hit-list,
.symbol-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}

.hit,
.symbol {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  padding: 0.7rem 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-width: 0;
}

.hit-head,
.symbol-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.hit-path,
.symbol-name {
  font-weight: 650;
  color: var(--color-navy);
  word-break: break-all;
}

.badge-lines {
  background: var(--color-canvas);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
}

.badge-kind {
  background: #eef2ff;
  border: 1px solid #c7d2fe;
  color: #3730a3;
}

.badge-exported {
  background: var(--color-ok-soft);
  color: var(--color-ok);
  border: 1px solid rgb(28 124 70 / 30%);
}

.badge-private {
  background: #f3f4f6;
  color: var(--color-text-muted);
  border: 1px solid var(--color-gray-line);
}

.hit-preview,
.symbol-signature {
  margin: 0;
  padding: 0.55rem 0.7rem;
  background: var(--color-canvas);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.hit-match,
.symbol-meta {
  margin: 0;
  font-size: 0.76rem;
  color: var(--color-text-muted);
}

.file-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.84rem;
}

.file-table th {
  text-align: left;
  padding: 0.45rem 0.6rem;
  border-bottom: 1px solid var(--color-gray-line);
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
}

.file-table td {
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid var(--color-gray-line);
  vertical-align: top;
}

.file-table tbody tr:last-child td {
  border-bottom: none;
}

.file-path {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.skeleton {
  height: 5rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
