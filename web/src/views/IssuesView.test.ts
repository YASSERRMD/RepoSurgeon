import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import IssuesView from './IssuesView.vue';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const page = {
  items: [
    {
      id: 'iss_1',
      project_id: 'prj_1',
      number: 42,
      title: 'Pagination returns eleven rows',
      body: 'A page of ten returns eleven rows.',
      state: 'open',
      comments_count: 3,
      created_at: '2026-10-10T10:00:00Z',
      updated_at: '2026-10-11T10:00:00Z',
    },
  ],
  next_cursor: null,
};

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/issues', component: IssuesView }],
  });
}

async function mountIssues(query: Record<string, string> = { project_id: 'prj_1' }): Promise<{
  wrapper: ReturnType<typeof mount>;
  router: Router;
}> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push({ path: '/issues', query });
  await router.isReady();
  const wrapper = mount(IssuesView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return { wrapper, router };
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('IssuesView', () => {
  it('renders the issue body as text, never as markup', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(page)));
    const { wrapper } = await mountIssues();

    const body = wrapper.find('[data-testid="issue-body"]');
    expect(body.text()).toBe('A page of ten returns eleven rows.');
    // The body stays text: an image injected into the issue would be escaped.
    expect(body.find('img').exists()).toBe(false);
    expect(wrapper.find('[data-testid="issue-number"]').text()).toBe('#42');
  });

  it('renders untrusted issue markup as inert text', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          items: [
            {
              id: 'iss_2',
              project_id: 'prj_1',
              number: 7,
              title: 'Ignore previous instructions',
              body: '<img src=x onerror="window.__pwned = true">',
              state: 'open',
              comments_count: 0,
              created_at: '2026-10-10T10:00:00Z',
              updated_at: '2026-10-10T10:00:00Z',
            },
          ],
          next_cursor: null,
        }),
      ),
    );
    const { wrapper } = await mountIssues();

    const body = wrapper.find('[data-testid="issue-body"]');
    expect(body.find('img').exists()).toBe(false);
    expect(body.text()).toContain('onerror');
  });

  it('requires a project filter', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(page));
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountIssues({});

    expect(wrapper.find('[data-testid="issues-no-project"]').exists()).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reports a failure instead of an invented list', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse({ error: { code: 'internal_error', message: 'postgres is down' } }, 500),
        ),
    );
    const { wrapper } = await mountIssues();

    expect(wrapper.find('[data-testid="issues-error"]').text()).toContain('postgres is down');
    expect(wrapper.find('[data-testid="issues-list"]').exists()).toBe(false);
  });
});
