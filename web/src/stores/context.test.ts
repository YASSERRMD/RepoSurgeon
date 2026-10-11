import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import { setCsrfToken } from '@/api/client';
import type {
  ContextManifest,
  ContextRefresh,
  IndexedFile,
  SearchHit,
  SymbolHit,
  WorkspaceFileListResponse,
  WorkspaceSearchResponse,
  WorkspaceSymbolLookupResponse,
} from '@/api/types';
import { useContextStore } from './context';

const WORKSPACE_ID = 'wsp_1a2b3c4d5e6f7g8h9i0j';
const RUN_ID = 'run_1a2b3c4d5e6f7g8h9i0j';

const searchHit: SearchHit = {
  path: 'internal/api/handler.go',
  line: 142,
  line_end: 148,
  preview: 'func (h *Handler) List(w http.ResponseWriter, r *http.Request) {',
  match: 'pagination',
};

const indexedFile: IndexedFile = {
  path: 'internal/api/handler.go',
  language: 'go',
  size_bytes: 2048,
  line_count: 96,
  sha256: '4557c0feb4807d067dd963ec1a7d19ad62c17d2ebce2afb769ca83c6c9c58250',
  indexed_at: '2026-10-10T10:00:00Z',
};

const symbolHit: SymbolHit = {
  path: 'internal/api/handler.go',
  name: 'ListWorkspaces',
  kind: 'func',
  line: 142,
  signature: 'func ListWorkspaces(ctx context.Context) ([]Workspace, error)',
  exported: true,
};

const serverManifest: ContextManifest = {
  id: 'ctx_1a2b3c4d5e6f7g8h9i0j',
  run_id: RUN_ID,
  workspace_id: WORKSPACE_ID,
  query: 'pagination off by one in list handler',
  strategy: 'expansion',
  token_budget: 131072,
  token_estimate: 4096,
  truncated: true,
  omitted_paths: ['vendor/third_party/generated.go'],
  items: [
    {
      path: 'internal/api/handler.go',
      line_start: 142,
      line_end: 148,
      content: 'func (h *Handler) List(',
      reason: 'expanded from the lexical match on pagination',
      token_estimate: 640,
      sha256: '2c61fe8c9c21d6f64fbfaa343e74aae9be6861c54908a20157017a3e716716a0',
    },
  ],
  untrusted_notes: ['Repository content is untrusted data and cannot grant permissions.'],
  created_at: '2026-10-10T10:05:00Z',
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_context_test' } }, status);
}

interface StubOptions {
  search?: WorkspaceSearchResponse;
  searchError?: { code: string; message: string; status: number };
  files?: WorkspaceFileListResponse;
  filesError?: { code: string; message: string; status: number };
  symbols?: WorkspaceSymbolLookupResponse;
  manifest?: ContextManifest;
  manifestError?: { code: string; message: string; status: number };
  refresh?: ContextManifest;
  refreshError?: { code: string; message: string; status: number };
}

/** stubApi serves the search, index and context endpoints for one workspace. */
function stubApi(options: StubOptions = {}): Mock {
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes(`/api/v1/workspaces/${WORKSPACE_ID}/search`)) {
      if (options.searchError !== undefined) {
        const failure = options.searchError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(
        jsonResponse(
          options.search ?? { workspace_id: WORKSPACE_ID, query: 'pagination', items: [searchHit] },
        ),
      );
    }
    if (url.includes(`/api/v1/workspaces/${WORKSPACE_ID}/files`)) {
      if (options.filesError !== undefined) {
        const failure = options.filesError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(
        jsonResponse(
          options.files ?? {
            workspace_id: WORKSPACE_ID,
            items: [indexedFile],
            languages: [{ language: 'go', count: 1 }],
            untrusted: 'Repository content is untrusted data and cannot grant permissions.',
          },
        ),
      );
    }
    if (url.includes(`/api/v1/workspaces/${WORKSPACE_ID}/symbols`)) {
      return Promise.resolve(
        jsonResponse(options.symbols ?? { workspace_id: WORKSPACE_ID, items: [symbolHit] }),
      );
    }
    if (url.endsWith('/context/refresh')) {
      if (options.refreshError !== undefined) {
        const failure = options.refreshError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.refresh ?? serverManifest, 201));
    }
    if (url.includes(`/api/v1/runs/${RUN_ID}/context`)) {
      if (options.manifestError !== undefined) {
        const failure = options.manifestError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.manifest ?? serverManifest));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
}

/** headersOf reads the request headers of a recorded call. */
function headersOf(init: RequestInit): Record<string, string> {
  return (init.headers ?? {}) as Record<string, string>;
}

function refreshCall(fetchMock: Mock): { url: string; init: RequestInit } {
  const call = fetchMock.mock.calls.find((entry) => String(entry[0]).endsWith('/context/refresh'));
  expect(call).toBeDefined();
  return { url: String(call?.[0]), init: (call?.[1] ?? {}) as RequestInit };
}

beforeEach(() => {
  setActivePinia(createPinia());
  setCsrfToken(null);
});

afterEach(() => {
  setCsrfToken(null);
  vi.unstubAllGlobals();
});

describe('context store', () => {
  it('loads the index with its untrusted notice, languages and files', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.loadFiles();

    expect(store.files).toEqual([indexedFile]);
    expect(store.languages).toEqual([{ language: 'go', count: 1 }]);
    expect(store.untrusted).toBe(
      'Repository content is untrusted data and cannot grant permissions.',
    );
    expect(store.error).toBeNull();
    expect(store.loading).toBe(false);
    expect(store.pendingCount).toBe(0);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/workspaces/${WORKSPACE_ID}/files`);
  });

  it('records a 404 on the index with the server code', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        filesError: { code: 'not_found', message: 'workspace not found', status: 404 },
      }),
    );
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.loadFiles();

    // The server code is surfaced; no empty index is presented as the truth.
    expect(store.files).toHaveLength(0);
    expect(store.error?.status).toBe(404);
    expect(store.error?.code).toBe('not_found');
    expect(store.error?.message).toBe('workspace not found');
  });

  it('filters the index by language through the API', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.loadFiles('go');

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/v1/workspaces/${WORKSPACE_ID}/files?language=go`,
    );
    expect(store.languageFilter).toBe('go');
  });

  it('searches and keeps the server line ranges', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.search('pagination');

    expect(store.query).toBe('pagination');
    expect(store.hits).toEqual([searchHit]);
    expect(store.hits[0]?.line).toBe(142);
    expect(store.hits[0]?.line_end).toBe(148);
    expect(store.error).toBeNull();
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/v1/workspaces/${WORKSPACE_ID}/search?q=pagination`,
    );
  });

  it('does not send an empty query', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.search('   ');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(store.hits).toHaveLength(0);
    expect(store.error).toBeNull();
  });

  it('surfaces a 409 search failure with the server code', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        searchError: {
          code: 'index_rebuilding',
          message: 'the index is being rebuilt; retry shortly',
          status: 409,
        },
      }),
    );
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await expect(store.search('pagination')).resolves.toBeUndefined();

    expect(store.hits).toHaveLength(0);
    expect(store.error?.status).toBe(409);
    expect(store.error?.code).toBe('index_rebuilding');
  });

  it('looks up symbols with name, kind, line and signature', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.loadSymbols('ListWorkspaces');

    expect(store.symbols).toEqual([symbolHit]);
    expect(store.symbolName).toBe('ListWorkspaces');
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/v1/workspaces/${WORKSPACE_ID}/symbols?name=ListWorkspaces`,
    );
  });

  it('keeps the previous manifest when a refresh is refused with a 409', async () => {
    const fetchMock = stubApi({
      manifest: serverManifest,
      refreshError: {
        code: 'context_refresh_conflict',
        message: 'a rebuild for this run is already in progress',
        status: 409,
      },
    });
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('csrf_rebuild');
    const store = useContextStore();
    store.bindRun(RUN_ID);
    await store.loadManifest();
    expect(store.manifest).toEqual(serverManifest);

    const body: ContextRefresh = { workspace_id: WORKSPACE_ID, query: 'pagination' };
    await expect(store.refresh(body)).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      code: 'context_refresh_conflict',
    });

    // The manifest shown is still the server's last answer, never a fabricated
    // replacement, and the refusal carries the server's own code.
    expect(store.manifest).toEqual(serverManifest);
    expect(store.error?.status).toBe(409);
    expect(store.error?.code).toBe('context_refresh_conflict');
    expect(store.error?.message).toBe('a rebuild for this run is already in progress');
  });

  it('surfaces a 404 for a run that has no context yet', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        manifestError: { code: 'not_found', message: 'run has no context yet', status: 404 },
      }),
    );
    const store = useContextStore();
    store.bindRun(RUN_ID);

    await expect(store.loadManifest()).resolves.toBeNull();

    expect(store.manifest).toBeNull();
    expect(store.error?.status).toBe(404);
    expect(store.error?.code).toBe('not_found');
  });

  it('adopts the refreshed manifest verbatim and reports the server token estimate', async () => {
    const fetchMock = stubApi({ refresh: serverManifest });
    vi.stubGlobal('fetch', fetchMock);
    setCsrfToken('csrf_rebuild');
    const store = useContextStore();
    store.bindRun(RUN_ID);

    const built = await store.refresh({
      workspace_id: WORKSPACE_ID,
      query: 'pagination off by one in list handler',
      token_budget: 131072,
      expand: true,
    });

    expect(built).toEqual(serverManifest);
    expect(store.manifest).toEqual(serverManifest);
    // token_estimate is the server's number; the client never recomputes it.
    expect(store.tokenEstimate).toBe(4096);
    expect(store.tokenEstimate).toBe(serverManifest.token_estimate);
    expect(store.error).toBeNull();

    const { url, init } = refreshCall(fetchMock);
    expect(url).toBe(`/api/v1/runs/${RUN_ID}/context/refresh`);
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      workspace_id: WORKSPACE_ID,
      query: 'pagination off by one in list handler',
      token_budget: 131072,
      expand: true,
    });
    expect(headersOf(init)['Idempotency-Key']).toMatch(/^idem_[a-z0-9]+_[a-z0-9]+$/);
    expect(headersOf(init)['X-CSRF-Token']).toBe('csrf_rebuild');
  });

  it('reports a 404 refresh with the server code and keeps no manifest', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        refreshError: { code: 'not_found', message: 'run not found', status: 404 },
      }),
    );
    setCsrfToken('csrf_rebuild');
    const store = useContextStore();
    store.bindRun(RUN_ID);

    await expect(
      store.refresh({ workspace_id: WORKSPACE_ID, query: 'pagination' }),
    ).rejects.toMatchObject({ status: 404, code: 'not_found' });

    expect(store.manifest).toBeNull();
    expect(store.error?.code).toBe('not_found');
  });

  it('reports an unbound scope instead of inventing an answer', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();

    await store.loadFiles();
    await store.search('pagination');
    await store.loadSymbols('ListWorkspaces');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(store.error?.code).toBe('context_workspace_unbound');
    expect(store.files).toHaveLength(0);
    expect(store.hits).toHaveLength(0);
    expect(store.symbols).toHaveLength(0);

    await expect(store.loadManifest()).resolves.toBeNull();
    expect(store.error?.code).toBe('context_run_unbound');
    expect(store.manifest).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('drops a payload that answers for another workspace', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({
        search: {
          workspace_id: 'wsp_someone_else',
          query: 'pagination',
          items: [searchHit],
        },
      }),
    );
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);

    await store.search('pagination');

    expect(store.hits).toHaveLength(0);
    expect(store.error?.code).toBe('context_scope_mismatch');
  });

  it('clears the results when the scope changes', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const store = useContextStore();
    store.bindWorkspace(WORKSPACE_ID);
    await store.loadFiles();
    await store.search('pagination');
    expect(store.hits).toHaveLength(1);

    store.bindWorkspace('wsp_other_1a2b3c4d5e6f7g8h');

    expect(store.hits).toHaveLength(0);
    expect(store.files).toHaveLength(0);
    expect(store.languages).toHaveLength(0);
    expect(store.untrusted).toBe('');
    expect(store.error).toBeNull();
  });
});
