import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import SettingsView from './SettingsView.vue';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/settings', component: SettingsView }],
  });
}

async function mountSettings(): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push('/settings');
  await router.isReady();
  const wrapper = mount(SettingsView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('SettingsView', () => {
  it('stores the settings through the API and reports the stored time', async () => {
    let stored: Record<string, unknown> = {};
    const fetchMock = vi.fn((_input: string, init?: RequestInit): Promise<Response> => {
      if (init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as { view: Record<string, unknown> };
        stored = body.view;
        return Promise.resolve(
          jsonResponse({ user_id: 'usr_1', view: stored, updated_at: '2026-10-10T11:00:00Z' }),
        );
      }
      return Promise.resolve(jsonResponse({ user_id: 'usr_1', view: stored, stored: true }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const wrapper = await mountSettings();
    await wrapper.find('[data-testid="settings-density-select"]').setValue('compact');
    await wrapper.find('[data-testid="settings-save"]').trigger('click');
    await flushPromises();

    expect(stored['table_density']).toBe('compact');
    expect(wrapper.find('[data-testid="settings-saved"]').exists()).toBe(true);
  });

  it('restores the stored settings on load', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          user_id: 'usr_1',
          view: { table_density: 'compact', active_section: 'runs' },
          stored: true,
          updated_at: '2026-10-10T11:00:00Z',
        }),
      ),
    );
    const wrapper = await mountSettings();

    const density = wrapper.find('[data-testid="settings-density-select"]')
      .element as HTMLSelectElement;
    expect(density.value).toBe('compact');
  });

  it('reports a storage failure instead of claiming the settings were saved', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((_input: string, init?: RequestInit): Promise<Response> => {
        if (init?.method === 'PUT') {
          return Promise.resolve(
            jsonResponse({ error: { code: 'validation_failed', message: 'too large' } }, 400),
          );
        }
        return Promise.resolve(jsonResponse({ user_id: 'usr_1', view: {}, stored: false }));
      }),
    );
    const wrapper = await mountSettings();

    await wrapper.find('[data-testid="settings-save"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="settings-error"]').text()).toContain('too large');
    expect(wrapper.find('[data-testid="settings-saved"]').exists()).toBe(false);
  });

  it('starts from the defaults when nothing is stored', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ user_id: 'usr_1', view: {}, stored: false })),
    );
    const wrapper = await mountSettings();

    const density = wrapper.find('[data-testid="settings-density-select"]')
      .element as HTMLSelectElement;
    expect(density.value).toBe('comfortable');
    expect(wrapper.find('[data-testid="settings-saved"]').exists()).toBe(false);
  });
});
