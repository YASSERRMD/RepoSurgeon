import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';

import HomeView from './HomeView.vue';
import type { ReadinessReport, SystemInfo } from '@/api/types';

const system: SystemInfo = {
  name: 'reposurgeon',
  version: '1.2.3',
  git_commit: 'deadbeef',
  build_time: '2026-10-10T10:00:00Z',
  environment: 'development',
  go_version: 'go1.25.5',
  started_at: '2026-10-10T09:59:00Z',
  uptime_seconds: 95,
};

const ready: ReadinessReport = {
  status: 'ready',
  checks: [
    { name: 'database', status: 'up', duration_ms: 2 },
    { name: 'migrations', status: 'up', duration_ms: 1 },
  ],
  generated_at: '2026-10-10T10:00:00Z',
};

const unavailable: ReadinessReport = {
  status: 'unavailable',
  checks: [
    {
      name: 'database',
      status: 'down',
      detail: 'database unreachable; run make dev',
      duration_ms: 5,
    },
  ],
  generated_at: '2026-10-10T10:00:00Z',
};

function stubFetch(responses: {
  ready?: ReadinessReport;
  system?: SystemInfo;
  reject?: boolean;
}): void {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockImplementation((input: string) => {
      if (responses.reject) return Promise.reject(new Error('ECONNREFUSED'));
      if (input.includes('/health/ready')) {
        const body = responses.ready ?? ready;
        return Promise.resolve(
          new Response(JSON.stringify(body), {
            status: body.status === 'unavailable' ? 503 : 200,
            headers: { 'Content-Type': 'application/json' },
          }),
        );
      }
      if (input.includes('/api/v1/system/info')) {
        return Promise.resolve(
          new Response(JSON.stringify(responses.system ?? system), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }),
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify({ status: 'ok', uptime_seconds: 1 }), { status: 200 }),
      );
    }),
  );
}

async function mountHome(): Promise<ReturnType<typeof mount>> {
  const wrapper = mount(HomeView, {
    global: { plugins: [createPinia()] },
  });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('HomeView', () => {
  it('renders measured readiness checks and build facts', async () => {
    stubFetch({});
    const wrapper = await mountHome();

    expect(wrapper.find('[data-testid="readiness-card"]').text()).toContain('up');
    expect(wrapper.findAll('.check-row')).toHaveLength(2);
    expect(wrapper.find('[data-testid="fact-version"]').text()).toBe('1.2.3');
    expect(wrapper.find('[data-testid="fact-uptime"]').text()).toBe('1m 35s');
    expect(wrapper.find('[data-testid="error-banner"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="remediation"]').exists()).toBe(false);
  });

  it('shows remediation guidance when a dependency is down', async () => {
    stubFetch({ ready: unavailable });
    const wrapper = await mountHome();

    const remediation = wrapper.find('[data-testid="remediation"]');
    expect(remediation.exists()).toBe(true);
    expect(remediation.text()).toContain('database');
    expect(remediation.text()).toContain('make dev');
    expect(wrapper.find('[data-testid="readiness-card"]').text()).toContain('down');
  });

  it('shows an actionable error banner when the API is unreachable', async () => {
    stubFetch({ reject: true });
    const wrapper = await mountHome();

    const banner = wrapper.find('[data-testid="error-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('ECONNREFUSED');
    expect(wrapper.find('[data-testid="remediation"]').exists()).toBe(false);
  });

  it('refreshes on demand and reflects new probe data', async () => {
    stubFetch({});
    const wrapper = await mountHome();
    expect(wrapper.find('[data-testid="fact-version"]').text()).toBe('1.2.3');

    stubFetch({ system: { ...system, version: '9.9.9' } });
    await wrapper.find('button.refresh').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="fact-version"]').text()).toBe('9.9.9');
  });
});
