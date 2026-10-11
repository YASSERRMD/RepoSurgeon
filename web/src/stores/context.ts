import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { ApiError, createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type {
  ContextManifest,
  ContextRefresh,
  IndexedFile,
  LanguageSummary,
  SearchHit,
  SymbolHit,
} from '@/api/types';

/**
 * ContextFailure keeps the server's error envelope intact. A 409 (the manifest
 * could not be built under those constraints) or a 404 (no manifest for that
 * run yet) is reported with the code the API sent, so the UI can explain the
 * refusal instead of inventing a manifest or an empty search state.
 */
export interface ContextFailure {
  status: number;
  code: string;
  message: string;
  requestId: string;
  retryable: boolean;
}

/** failureOf converts a thrown error into the failure the views render. */
export function failureOf(cause: unknown): ContextFailure {
  if (cause instanceof ApiError) {
    return {
      status: cause.status,
      code: cause.code,
      message: cause.message,
      requestId: cause.requestId,
      retryable: cause.retryable,
    };
  }
  return {
    status: 0,
    code: 'client_error',
    message: cause instanceof Error ? cause.message : String(cause),
    requestId: '',
    retryable: false,
  };
}

function unboundWorkspace(): ContextFailure {
  return {
    status: 0,
    code: 'context_workspace_unbound',
    message: 'no workspace is selected, so there is nothing to search',
    requestId: '',
    retryable: false,
  };
}

function unboundRun(): ContextFailure {
  return {
    status: 0,
    code: 'context_run_unbound',
    message: 'no run is selected, so there is no context manifest to read',
    requestId: '',
    retryable: false,
  };
}

/** scopeMismatch is reported when the API answers for a different identifier. */
function scopeMismatch(expected: string, answeredFor: string): ContextFailure {
  return {
    status: 200,
    code: 'context_scope_mismatch',
    message: `the API answered for ${answeredFor} instead of ${expected}; nothing was adopted`,
    requestId: '',
    retryable: false,
  };
}

/**
 * The context store owns code search, the file index and the run context
 * manifest. Every field is a copy of what the server returned: hits are never
 * synthesised locally, token estimates are never recomputed in the browser, and
 * repository content stays untrusted data — it is displayed, never treated as
 * an instruction or as a grant of permissions.
 */
export const useContextStore = defineStore('context', () => {
  const client: HealthClient = createHealthClient();
  const workspaceId = ref<string | null>(null);
  const runId = ref<string | null>(null);
  const hits = ref<SearchHit[]>([]);
  const files = ref<IndexedFile[]>([]);
  const languages = ref<LanguageSummary[]>([]);
  const symbols = ref<SymbolHit[]>([]);
  const manifest = ref<ContextManifest | null>(null);
  /** untrusted is the notice the index returns about repository content. */
  const untrusted = ref('');
  const languageFilter = ref('');
  const query = ref('');
  const symbolName = ref('');
  const loading = ref(false);
  const error = ref<ContextFailure | null>(null);
  const inFlight = ref(0);

  /** pendingCount counts the requests still outstanding for this inspector. */
  const pendingCount = computed(() => inFlight.value);

  /** tokenEstimate is the server's own estimate; the client never recomputes it. */
  const tokenEstimate = computed(() => manifest.value?.token_estimate ?? 0);

  /** tracked wraps one call so loading and pendingCount follow real requests. */
  async function tracked<T>(work: () => Promise<T>): Promise<T> {
    inFlight.value += 1;
    loading.value = true;
    try {
      return await work();
    } finally {
      inFlight.value -= 1;
      loading.value = inFlight.value > 0;
    }
  }

  /** bindWorkspace selects the workspace the search side reads. */
  function bindWorkspace(id: string | null): void {
    workspaceId.value = id === '' ? null : id;
    hits.value = [];
    files.value = [];
    languages.value = [];
    symbols.value = [];
    untrusted.value = '';
    languageFilter.value = '';
    query.value = '';
    symbolName.value = '';
    error.value = null;
  }

  /** bindRun selects the run whose context manifest is inspected. */
  function bindRun(id: string | null): void {
    runId.value = id === '' ? null : id;
    manifest.value = null;
    error.value = null;
  }

  /**
   * loadFiles lists the indexed files and the language summary. The untrusted
   * notice travels with the index, so a successful load is what makes the
   * warning visible.
   */
  async function loadFiles(language?: string): Promise<void> {
    const id = workspaceId.value;
    if (id === null) {
      files.value = [];
      error.value = unboundWorkspace();
      return;
    }
    const filter = language ?? languageFilter.value;
    languageFilter.value = filter;
    try {
      const page = await tracked(() =>
        client.listWorkspaceFiles(id, filter === '' ? undefined : filter),
      );
      // A response for another workspace is dropped rather than displayed.
      if (workspaceId.value !== id || page.workspace_id !== id) {
        files.value = [];
        error.value = scopeMismatch(id, page.workspace_id);
        return;
      }
      files.value = page.items;
      languages.value = page.languages;
      untrusted.value = page.untrusted;
      error.value = null;
    } catch (cause) {
      files.value = [];
      error.value = failureOf(cause);
    }
  }

  /** search runs the query the caller typed; an empty query is not sent. */
  async function search(term: string): Promise<void> {
    const q = term.trim();
    if (q === '') return;
    const id = workspaceId.value;
    if (id === null) {
      hits.value = [];
      error.value = unboundWorkspace();
      return;
    }
    try {
      const page = await tracked(() => client.searchWorkspace(id, q));
      if (workspaceId.value !== id || page.workspace_id !== id) {
        hits.value = [];
        error.value = scopeMismatch(id, page.workspace_id);
        return;
      }
      hits.value = page.items;
      query.value = page.query;
      error.value = null;
    } catch (cause) {
      hits.value = [];
      error.value = failureOf(cause);
    }
  }

  /** loadSymbols looks up declared symbols by name. */
  async function loadSymbols(name: string): Promise<void> {
    const symbol = name.trim();
    if (symbol === '') {
      symbols.value = [];
      symbolName.value = '';
      return;
    }
    const id = workspaceId.value;
    if (id === null) {
      symbols.value = [];
      error.value = unboundWorkspace();
      return;
    }
    try {
      const page = await tracked(() => client.lookupWorkspaceSymbols(id, symbol));
      if (workspaceId.value !== id || page.workspace_id !== id) {
        symbols.value = [];
        error.value = scopeMismatch(id, page.workspace_id);
        return;
      }
      symbols.value = page.items;
      symbolName.value = symbol;
      error.value = null;
    } catch (cause) {
      symbols.value = [];
      error.value = failureOf(cause);
    }
  }

  /** loadManifest reads the manifest the run is currently using. */
  async function loadManifest(): Promise<ContextManifest | null> {
    const id = runId.value;
    if (id === null) {
      manifest.value = null;
      error.value = unboundRun();
      return null;
    }
    try {
      const loaded = await tracked(() => client.getRunContext(id));
      if (runId.value !== id) return null;
      manifest.value = loaded;
      error.value = null;
      return loaded;
    } catch (cause) {
      // The previous manifest stays visible: it is the last server answer, and
      // replacing it with an empty state would hide what the run really used.
      error.value = failureOf(cause);
      return null;
    }
  }

  /**
   * refresh asks the server to rebuild the context and adopts the manifest it
   * records. A 409 keeps the existing manifest and surfaces the server code so
   * the caller can explain the refusal instead of showing a fabricated one.
   */
  async function refresh(body: ContextRefresh): Promise<ContextManifest | null> {
    const id = runId.value;
    if (id === null) {
      error.value = unboundRun();
      return null;
    }
    try {
      const built = await tracked(() => client.refreshRunContext(id, body, newIdempotencyKey()));
      if (runId.value !== id) return null;
      manifest.value = built;
      error.value = null;
      return built;
    } catch (cause) {
      error.value = failureOf(cause);
      throw cause;
    }
  }

  function clearError(): void {
    error.value = null;
  }

  /** reset drops every result; a new scope starts from a clean inspector. */
  function reset(): void {
    bindWorkspace(null);
    bindRun(null);
  }

  return {
    client,
    workspaceId,
    runId,
    hits,
    files,
    languages,
    symbols,
    manifest,
    untrusted,
    languageFilter,
    query,
    symbolName,
    loading,
    error,
    pendingCount,
    tokenEstimate,
    bindWorkspace,
    bindRun,
    loadFiles,
    search,
    loadSymbols,
    loadManifest,
    refresh,
    clearError,
    reset,
  };
});
