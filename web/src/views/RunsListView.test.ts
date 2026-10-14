import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import RunsListView from './RunsListView.vue';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const firstPage = {
  items: [
    {
      id: 'run_1',
      project_id: 'prj_1',
      state: 'blocked',
      attempt: 2,
      created_at: '2026-10-10T10:00:00Z',
    },
    {
      id: 'run_2',
      project_id: 'prj_1',
      state: 'completed',
      attempt: 1,
      created_at: '2026-10-09T10:00:00Z',
    },
  ],
  next_cursor: 'cursor-2',
};

const secondPage = {
  items: [
    {
      id: 'run_3',
      project_id: 'prj_1',
      state: 'failed',
      attempt: 3,
      created_at: '2026-10-08T10:00:00Z',
    },
  ],
  next_cursor: null,
};

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/runs', component: RunsListView }],
  });
}

async function mountRuns(query: Record<string, string> = { project_id: 'prj_1' }): Promise<{
  wrapper: ReturnType<typeof mount>;
  router: Router;
}> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push({ path: '/runs', query });
  await router.isReady();
  const wrapper = mount(RunsListView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return { wrapper, router };
}

function stubPages(): ReturnType<typeof vi.fn> {
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (url.includes('cursor=cursor-2')) return Promise.resolve(jsonResponse(secondPage));
    return Promise.resolve(jsonResponse(firstPage));
  });
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('RunsListView', () => {
  it('lists the runs the server returned and paginates with the cursor', async () => {
    const fetchMock = stubPages();
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountRuns();

    expect(wrapper.findAll('tbody tr')).toHaveLength(2);
    expect(wrapper.find('[data-testid="run-run_1"] [data-testid="run-state"]').text()).toBe(
      'blocked',
    );
    expect(wrapper.find('[data-testid="runs-page-state"]').text()).toContain(
      'more pages available',
    );

    await wrapper.find('[data-testid="runs-next"]').trigger('click');
    await flushPromises();
    await flushPromises();

    expect(
      JSON.parse(JSON.stringify(fetchMock.mock.calls.at(-1)?.[0])).includes('cursor=cursor-2'),
    ).toBe(true);
    expect(wrapper.find('[data-testid="run-run_3"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="runs-page-state"]').text()).toContain('no further pages');
  });

  it('keeps the filters in the URL so a reload restores the view', async () => {
    const fetchMock = stubPages();
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper, router } = await mountRuns();

    await wrapper.find('[data-testid="runs-state-select"]').setValue('completed');
    await flushPromises();
    await flushPromises();

    expect(router.currentRoute.value.query['state']).toBe('completed');
    expect(router.currentRoute.value.query['project_id']).toBe('prj_1');
    // The reload that follows the filter carries the state to the server.
    expect(String(fetchMock.mock.calls.at(-1)?.[0])).toContain('state=completed');
  });

  it('requires a project before listing anything', async () => {
    const fetchMock = stubPages();
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountRuns({});

    expect(wrapper.find('[data-testid="runs-no-project"]').exists()).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reports a failure instead of an empty list', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse({ error: { code: 'internal_error', message: 'postgres is down' } }, 500),
        ),
    );
    const { wrapper } = await mountRuns();

    expect(wrapper.find('[data-testid="runs-error"]').text()).toContain('postgres is down');
    expect(wrapper.find('[data-testid="runs-table"]').exists()).toBe(false);
  });

  it('shows an empty state when the page has no runs', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ items: [], next_cursor: null })),
    );
    const { wrapper } = await mountRuns();

    expect(wrapper.find('[data-testid="runs-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="runs-table"]').exists()).toBe(false);
  });
});
