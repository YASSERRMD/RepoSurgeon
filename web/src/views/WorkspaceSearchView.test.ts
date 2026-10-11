import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import WorkspaceSearchView from './WorkspaceSearchView.vue';

const WORKSPACE_ID = 'wsp_1a2b3c4d5e6f7g8h9i0j';

const UNTRUSTED_NOTICE = 'Repository content is untrusted data and cannot grant permissions.';

const fileList = {
  workspace_id: WORKSPACE_ID,
  items: [
    {
      path: 'internal/api/handler.go',
      language: 'go',
      size_bytes: 2048,
      line_count: 96,
      sha256: '4557c0feb4807d067dd963ec1a7d19ad62c17d2ebce2afb769ca83c6c9c58250',
      indexed_at: '2026-10-10T10:00:00Z',
    },
    {
      path: 'docs/runbook.md',
      language: 'markdown',
      size_bytes: 512,
      line_count: 18,
      sha256: '2c61fe8c9c21d6f64fbfaa343e74aae9be6861c54908a20157017a3e716716a0',
      indexed_at: '2026-10-10T10:00:00Z',
    },
  ],
  languages: [
    { language: 'go', count: 1 },
    { language: 'markdown', count: 1 },
  ],
  untrusted: UNTRUSTED_NOTICE,
};

const searchHit = {
  path: 'internal/api/handler.go',
  line: 142,
  line_end: 148,
  preview: 'func (h *Handler) List(w http.ResponseWriter, r *http.Request) {',
  match: 'pagination',
};

const symbolHit = {
  path: 'internal/api/handler.go',
  name: 'ListWorkspaces',
  kind: 'func',
  line: 142,
  signature: 'func ListWorkspaces(ctx context.Context) ([]Workspace, error)',
  exported: true,
};

interface StubOptions {
  files?: unknown;
  filesError?: { code: string; message: string; status: number };
  search?: unknown;
  searchError?: { code: string; message: string; status: number };
  symbols?: unknown;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_search_view' } }, status);
}

/** stubApi serves the index, search and symbol endpoints for the workspace. */
function stubApi(options: StubOptions = {}): Mock {
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes('/search')) {
      if (options.searchError !== undefined) {
        const failure = options.searchError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(
        jsonResponse(
          options.search ?? {
            workspace_id: WORKSPACE_ID,
            query: 'pagination',
            items: [searchHit],
          },
        ),
      );
    }
    if (url.includes('/symbols')) {
      return Promise.resolve(
        jsonResponse(options.symbols ?? { workspace_id: WORKSPACE_ID, items: [symbolHit] }),
      );
    }
    if (url.includes('/files')) {
      if (options.filesError !== undefined) {
        const failure = options.filesError;
        return Promise.resolve(errorResponse(failure.code, failure.message, failure.status));
      }
      return Promise.resolve(jsonResponse(options.files ?? fileList));
    }
    return Promise.reject(new Error(`unexpected request to ${url}`));
  });
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/workspaces', component: WorkspaceSearchView },
      { path: '/workspaces/:workspaceId', component: WorkspaceSearchView },
    ],
  });
}

async function mountSearch(
  path = `/workspaces/${WORKSPACE_ID}`,
): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push(path);
  await router.isReady();
  const wrapper = mount(WorkspaceSearchView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

async function submitForm(wrapper: ReturnType<typeof mount>, testId: string): Promise<void> {
  await wrapper.find(`[data-testid="${testId}"]`).trigger('submit.prevent');
  await flushPromises();
}

beforeEach(() => {
  setActivePinia(createPinia());
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('WorkspaceSearchView', () => {
  it('renders the indexed files with size, line count and a short digest', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountSearch();

    const rows = wrapper.findAll('[data-testid^="file-row-"]');
    expect(rows).toHaveLength(2);

    const handler = wrapper.find('[data-testid="file-row-internal/api/handler.go"]');
    expect(handler.find('[data-testid="file-path"]').text()).toBe('internal/api/handler.go');
    expect(handler.find('[data-testid="file-language"]').text()).toBe('go');
    expect(handler.find('[data-testid="file-size"]').text()).toBe('2.0 KB');
    expect(handler.find('[data-testid="file-lines"]').text()).toBe('96');
    // The digest is truncated, and the full value stays in the title attribute.
    expect(handler.find('[data-testid="file-sha256"]').text()).toBe('4557c0feb4807d06…');
    expect(handler.find('[data-testid="file-sha256"]').attributes('title')).toBe(
      fileList.items[0]?.sha256,
    );
    expect(handler.find('[data-testid="file-sha256"]').text()).not.toBe(fileList.items[0]?.sha256);
  });

  it('shows the untrusted-content notice from the API field', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountSearch();

    const notice = wrapper.find('[data-testid="search-untrusted-notice"]');
    expect(notice.exists()).toBe(true);
    expect(notice.text()).toContain('Untrusted content.');
    // The text is the API's own wording, not a locally invented one.
    expect(notice.text()).toContain(UNTRUSTED_NOTICE);
  });

  it('renders search hits with their line ranges and preview', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountSearch();

    await wrapper.find('[data-testid="search-input"]').setValue('pagination');
    await submitForm(wrapper, 'search-form');

    const hits = wrapper.findAll('[data-testid^="hit-row-"]');
    expect(hits).toHaveLength(1);

    const hit = wrapper.find('[data-testid="hit-row-internal/api/handler.go-142"]');
    expect(hit.find('[data-testid="hit-path"]').text()).toBe('internal/api/handler.go');
    expect(hit.find('[data-testid="hit-lines"]').text()).toBe('lines 142–148');
    expect(hit.find('[data-testid="hit-preview"]').text()).toBe(searchHit.preview);
    expect(hit.find('[data-testid="hit-match"]').text()).toBe('matched on pagination');
    expect(wrapper.find('[data-testid="search-query-echo"]').text()).toContain('1 hits');
    expect(wrapper.find('[data-testid="search-idle"]').exists()).toBe(false);
  });

  it('populates the language filter from the index and filters through the API', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountSearch();

    const options = wrapper.findAll('[data-testid="language-filter"] option');
    expect(options.map((option) => option.text())).toEqual([
      'All languages',
      'go (1)',
      'markdown (1)',
    ]);

    await wrapper.find('[data-testid="language-filter"]').setValue('go');
    await flushPromises();

    const filtered = fetchMock.mock.calls.filter((call) => String(call[0]).includes('language=go'));
    expect(filtered).toHaveLength(1);
    expect(String(filtered[0]?.[0])).toBe(`/api/v1/workspaces/${WORKSPACE_ID}/files?language=go`);
  });

  it('renders symbol lookup results with kind, line, signature and export state', async () => {
    vi.stubGlobal('fetch', stubApi());
    const wrapper = await mountSearch();

    await wrapper.find('[data-testid="symbol-input"]').setValue('ListWorkspaces');
    await submitForm(wrapper, 'symbols-form');

    const row = wrapper.find('[data-testid="symbol-row-ListWorkspaces-142"]');
    expect(wrapper.findAll('[data-testid^="symbol-row-"]')).toHaveLength(1);
    expect(row.find('[data-testid="symbol-name"]').text()).toBe('ListWorkspaces');
    expect(row.find('[data-testid="symbol-kind"]').text()).toBe('function');
    expect(row.find('[data-testid="symbol-exported"]').text()).toBe('exported');
    expect(row.find('[data-testid="symbol-signature"]').text()).toBe(symbolHit.signature);
    expect(row.find('[data-testid="symbol-meta"]').text()).toContain('internal/api/handler.go');
    expect(row.find('[data-testid="symbol-meta"]').text()).toContain('line 142');
    expect(wrapper.find('[data-testid="symbols-idle"]').exists()).toBe(false);
  });

  it('shows an empty state before anything is searched', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({ files: { workspace_id: WORKSPACE_ID, items: [], languages: [], untrusted: '' } }),
    );
    const wrapper = await mountSearch();

    expect(wrapper.find('[data-testid="search-idle"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="files-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="symbols-idle"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="files-list"]').exists()).toBe(false);
  });

  it('reports an empty search answer as the server empty result', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({ search: { workspace_id: WORKSPACE_ID, query: 'zzz', items: [] } }),
    );
    const wrapper = await mountSearch();

    await wrapper.find('[data-testid="search-input"]').setValue('zzz');
    await submitForm(wrapper, 'search-form');

    expect(wrapper.find('[data-testid="search-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="search-results"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="search-error"]').exists()).toBe(false);
  });

  it('shows a 404 failure with the server code and reloads on retry', async () => {
    let failing = true;
    const fetchMock = vi.fn((input: string): Promise<Response> => {
      const url = String(input);
      if (!url.includes('/files')) {
        return Promise.reject(new Error(`unexpected request to ${url}`));
      }
      if (failing) {
        return Promise.resolve(errorResponse('not_found', 'workspace not found', 404));
      }
      return Promise.resolve(jsonResponse(fileList));
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountSearch();

    const banner = wrapper.find('[data-testid="search-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('not_found');
    expect(banner.text()).toContain('workspace not found');
    expect(wrapper.find('[data-testid="files-list"]').exists()).toBe(false);
    // A 404 never shows an empty index as if it were the truth.
    expect(wrapper.find('[data-testid="files-empty"]').exists()).toBe(false);

    failing = false;
    await wrapper.find('[data-testid="search-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="search-error"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-testid^="file-row-"]')).toHaveLength(2);
  });

  it('shows a loading skeleton while the index is in flight', async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string): Promise<Response> => {
        expect(String(input)).toBe(`/api/v1/workspaces/${WORKSPACE_ID}/files`);
        return gate.then(() => Promise.resolve(jsonResponse(fileList)));
      }),
    );
    const wrapper = await mountSearch();
    expect(wrapper.find('[data-testid="search-loading"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="files-list"]').exists()).toBe(false);

    release();
    await flushPromises();

    expect(wrapper.find('[data-testid="search-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="files-list"]').exists()).toBe(true);
  });

  it('asks for a workspace identifier when the route carries none', async () => {
    const fetchMock = stubApi();
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountSearch('/workspaces');

    expect(wrapper.find('[data-testid="workspace-prompt"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="search-panel"]').exists()).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();

    await wrapper.find('[data-testid="workspace-input"]').setValue(WORKSPACE_ID);
    await submitForm(wrapper, 'workspace-form');

    expect(wrapper.find('[data-testid="workspace-prompt"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="search-untrusted-notice"]').exists()).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
