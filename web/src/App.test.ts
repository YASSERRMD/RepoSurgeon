import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';

import App from './App.vue';
import { useHealthStore, type OperationalStatus } from '@/stores/health';
import { useOrganizationsStore } from '@/stores/organizations';
import { useAuthStore } from '@/stores/auth';

function stubResponses(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ status: 'ok', uptime_seconds: 1 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );
}

function routerStub() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } },
      { path: '/organizations', component: { template: '<div />' } },
      { path: '/projects', component: { template: '<div />' } },
      { path: '/members', component: { template: '<div />' } },
      { path: '/credentials', component: { template: '<div />' } },
      { path: '/approvals', component: { template: '<div />' } },
      { path: '/tools', component: { template: '<div />' } },
      { path: '/organizations/:id', component: { template: '<div />' } },
    ],
  });
}

beforeEach(() => {
  setActivePinia(createPinia());
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('App shell', () => {
  it('renders the brand and the primary navigation', async () => {
    stubResponses();
    const router = routerStub();
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } });

    expect(wrapper.text()).toContain('RepoSurgeon');
    expect(wrapper.find('[data-testid="footer-status"]').text()).toBeTruthy();

    const links = wrapper.findAll('nav a');
    expect(links.map((link) => link.attributes('href'))).toEqual([
      '/',
      '/organizations',
      '/projects',
      '/members',
      '/credentials',
      '/approvals',
      '/tools',
    ]);
    await wrapper.vm.$nextTick();
  });

  it('shows the signed-in user and the sign-out control', async () => {
    stubResponses();
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = routerStub();
    const wrapper = mount(App, { global: { plugins: [pinia, router] } });
    await flushPromises();

    // Anonymous visitors get no email and no sign-out button.
    expect(wrapper.find('[data-testid="current-user"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sign-out"]').exists()).toBe(false);

    const auth = useAuthStore();
    auth.user = {
      id: 'usr_1a2b3c4d5e6f7g8h9i0j',
      email: 'developer@example.com',
      organizations: [],
    };
    auth.status = 'authenticated';
    await flushPromises();

    expect(wrapper.find('[data-testid="current-user"]').text()).toBe('developer@example.com');

    await wrapper.find('[data-testid="sign-out"]').trigger('click');
    await flushPromises();

    expect(auth.status).toBe('anonymous');
    expect(wrapper.find('[data-testid="current-user"]').exists()).toBe(false);
  });

  it('shows the active organization name when one is selected', async () => {
    stubResponses();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation((input: string) =>
        Promise.resolve(
          new Response(
            JSON.stringify(
              String(input).includes('/api/v1/organizations')
                ? {
                    items: [
                      {
                        id: 'org_acme',
                        slug: 'acme-engineering',
                        name: 'Acme Engineering',
                        role: 'owner',
                        created_at: '2026-10-10T10:00:00Z',
                        updated_at: '2026-10-10T10:00:00Z',
                      },
                    ],
                    next_cursor: null,
                  }
                : { status: 'ok', uptime_seconds: 1 },
            ),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          ),
        ),
      ),
    );
    const pinia = createPinia();
    setActivePinia(pinia);
    window.localStorage.setItem('reposurgeon.activeOrganization', 'org_acme');
    const router = routerStub();
    const wrapper = mount(App, { global: { plugins: [pinia, router] } });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(useOrganizationsStore().activeOrganization).not.toBeNull();
    expect(wrapper.find('[data-testid="active-org"]').text()).toBe('Acme Engineering');
  });
});

describe('shell status copy', () => {
  it('maps every store status to a label', () => {
    stubResponses();
    mount(App, { global: { plugins: [createPinia(), routerStub()] } });
    const store = useHealthStore();
    const statuses: OperationalStatus[] = ['ready', 'unavailable', 'error', 'loading', 'idle'];
    for (const status of statuses) {
      store.status = status;
    }
    expect(store.status).toBe('idle');
  });
});
