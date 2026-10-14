import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import DashboardView from './DashboardView.vue';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const dashboard = {
  organization_id: 'org_acme',
  runs_by_state: { completed: 4, blocked: 2, failed: 1 },
  open_issues: 9,
  blocked_runs: 2,
  failed_runs: 1,
  recent_audit_events: 31,
  recent_runs: [
    {
      id: 'run_1a2b3c4d5e6f7g8h9i0j',
      project_id: 'prj_1a2b3c4d5e6f7g8h9i0j',
      project_name: 'RepoSurgeon',
      state: 'blocked',
      attempt: 2,
      created_at: '2026-10-10T10:00:00Z',
    },
  ],
  generated_at: '2026-10-10T10:05:00Z',
};

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: DashboardView }],
  });
}

async function mountDashboard(): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push('/');
  await router.isReady();
  const wrapper = mount(DashboardView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('DashboardView', () => {
  it('renders the aggregate the server computed', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(dashboard)));
    const wrapper = await mountDashboard();

    expect(wrapper.find('[data-testid="metric-open-issues"]').text()).toBe('9');
    expect(wrapper.find('[data-testid="metric-blocked-runs"]').text()).toBe('2');
    expect(wrapper.find('[data-testid="metric-failed-runs"]').text()).toBe('1');
    expect(wrapper.find('[data-testid="metric-audit-events"]').text()).toBe('31');
    // The total is the sum of the server's per-state counts, not a client guess.
    expect(wrapper.find('[data-testid="metric-total-runs"]').text()).toBe('7');
    expect(wrapper.find('[data-testid="state-blocked"] [data-testid="state-count"]').text()).toBe(
      '2',
    );
    expect(wrapper.find('[data-testid="recent-run_1a2b3c4d5e6f7g8h9i0j"]').exists()).toBe(true);
  });

  it('reports a failure instead of rendering invented counts', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse({ error: { code: 'internal_error', message: 'postgres is down' } }, 500),
        ),
    );
    const wrapper = await mountDashboard();

    expect(wrapper.find('[data-testid="dashboard-error"]').text()).toContain('postgres is down');
    expect(wrapper.find('[data-testid="metric-open-issues"]').exists()).toBe(false);
  });

  it('offers an empty state rather than a fake dashboard', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse({ error: { code: 'not_found', message: 'resource not found' } }, 404),
        ),
    );
    const wrapper = await mountDashboard();

    expect(wrapper.find('[data-testid="dashboard-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="metric-open-issues"]').exists()).toBe(false);
  });

  it('reloads the aggregate from the server', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(dashboard));
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountDashboard();

    await wrapper.find('[data-testid="dashboard-reload"]').trigger('click');
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('renders an empty organization honestly', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          organization_id: 'org_acme',
          runs_by_state: {},
          open_issues: 0,
          blocked_runs: 0,
          failed_runs: 0,
          recent_audit_events: 0,
          recent_runs: [],
          generated_at: '2026-10-10T10:05:00Z',
        }),
      ),
    );
    const wrapper = await mountDashboard();

    expect(wrapper.find('[data-testid="dashboard-states-none"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="dashboard-recent-none"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="metric-total-runs"]').text()).toBe('0');
  });
});
