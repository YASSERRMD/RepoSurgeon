import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import OrganizationsView from './OrganizationsView.vue';
import { useOrganizationsStore } from '@/stores/organizations';
import type { Organization, OrganizationPage } from '@/api/types';

const acme: Organization = {
  id: 'org_acme',
  slug: 'acme-engineering',
  name: 'Acme Engineering',
  role: 'owner',
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T10:00:00Z',
};

const globex: Organization = {
  id: 'org_globex',
  slug: 'globex',
  name: 'Globex',
  role: 'viewer',
  created_at: '2026-10-10T10:05:00Z',
  updated_at: '2026-10-10T10:05:00Z',
};

function page(items: Organization[]): OrganizationPage {
  return { items, next_cursor: null };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/organizations', component: { template: '<div />' } }],
  });
}

/** mountView installs one pinia instance for both the view and the test. */
async function mountView(): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push('/organizations');
  await router.isReady();
  const wrapper = mount(OrganizationsView, {
    global: { plugins: [pinia, router] },
  });
  await flushPromises();
  return wrapper;
}

/** stubOrganizations serves the organization collection; created toggles POST behaviour. */
function stubOrganizations(options: {
  list?: OrganizationPage;
  failList?: boolean;
  created?: Organization;
}): Mock {
  return vi.fn((_input: string, init?: RequestInit) => {
    if (init?.method === 'POST') {
      return Promise.resolve(jsonResponse(options.created ?? globex, 201));
    }
    if (options.failList) return Promise.reject(new Error('ECONNREFUSED'));
    return Promise.resolve(jsonResponse(options.list ?? page([acme, globex])));
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

describe('OrganizationsView', () => {
  it('renders organizations from the API with roles and the active marker', async () => {
    vi.stubGlobal('fetch', stubOrganizations({}));
    const wrapper = await mountView();

    const rows = wrapper.findAll('[data-testid^="org-org_"]');
    expect(rows).toHaveLength(2);
    expect(wrapper.find('[data-testid="org-org_acme"]').text()).toContain('Acme Engineering');
    expect(wrapper.find('[data-testid="org-org_acme"]').text()).toContain('owner');
    expect(wrapper.find('[data-testid="org-org_globex"]').text()).toContain('viewer');
    expect(wrapper.find('[data-testid="empty-organizations"]').exists()).toBe(false);
  });

  it('switches the active organization and persists it', async () => {
    vi.stubGlobal('fetch', stubOrganizations({}));
    const wrapper = await mountView();
    const organizations = useOrganizationsStore();

    await wrapper.find('[data-testid="select-org_globex"]').trigger('click');

    expect(organizations.activeOrganizationId).toBe('org_globex');
    expect(window.localStorage.getItem('reposurgeon.activeOrganization')).toBe('org_globex');
    expect(wrapper.find('[data-testid="org-org_globex"]').text()).toContain('active');
    expect(wrapper.find('[data-testid="select-org_globex"]').attributes('disabled')).toBeDefined();
  });

  it('shows an empty state when the caller has no organizations', async () => {
    vi.stubGlobal('fetch', stubOrganizations({ list: page([]) }));
    const wrapper = await mountView();

    const empty = wrapper.find('[data-testid="empty-organizations"]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('No organizations yet');
  });

  it('shows an error banner with retry when the API cannot be reached', async () => {
    const fetchMock = stubOrganizations({ failList: true });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountView();

    const banner = wrapper.find('[data-testid="org-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('ECONNREFUSED');
    expect(wrapper.find('[data-testid="org-list"]').exists()).toBe(false);

    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(page([acme]))));
    await wrapper.find('[data-testid="org-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="org-error"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-testid^="org-org_"]')).toHaveLength(1);
  });

  it('renders loading skeletons while the first page is in flight', async () => {
    let release: (response: Response) => void = () => undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            release = resolve;
          }),
      ),
    );
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = testRouter();
    const wrapper = mount(OrganizationsView, { global: { plugins: [pinia, router] } });
    await flushPromises();

    expect(wrapper.find('[data-testid="org-loading"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="org-list"]').exists()).toBe(false);

    release(jsonResponse(page([acme])));
    await flushPromises();

    expect(wrapper.find('[data-testid="org-loading"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-testid^="org-org_"]')).toHaveLength(1);
  });

  it('validates the create form before calling the API', async () => {
    const fetchMock = stubOrganizations({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountView();
    const callsBefore = fetchMock.mock.calls.length;

    await wrapper.find('#org-name').setValue('   ');
    await wrapper.find('#org-slug').setValue('Not A Slug');
    await wrapper.find('[data-testid="create-org-submit"]').trigger('submit');

    expect(fetchMock.mock.calls.length).toBe(callsBefore);
    expect(wrapper.find('[data-testid="org-name-error"]').text()).toContain('Name is required');
    expect(wrapper.find('[data-testid="org-slug-error"]').text()).toContain('lowercase letters');

    await wrapper.find('#org-name').setValue('Globex');
    await wrapper.find('#org-slug').setValue('gl');
    await wrapper.find('[data-testid="create-org-submit"]').trigger('submit');
    expect(fetchMock.mock.calls.length).toBe(callsBefore);
  });

  it('creates an organization with a trimmed body and an Idempotency-Key', async () => {
    const fetchMock = stubOrganizations({ created: globex });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountView();

    await wrapper.find('#org-name').setValue('Globex');
    await wrapper.find('#org-slug').setValue('globex');
    await wrapper.find('[data-testid="create-org-submit"]').trigger('submit');
    await flushPromises();

    const postCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'POST',
    );
    expect(postCall).toBeDefined();
    const init = (postCall?.[1] ?? {}) as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ name: 'Globex', slug: 'globex' });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^idem_/);

    const organizations = useOrganizationsStore();
    expect(organizations.activeOrganizationId).toBe('org_globex');
    expect(wrapper.find('[data-testid="org-org_globex"]').text()).toContain('active');
  });

  it('keeps the form and shows the API message when creation is rejected', async () => {
    const fetchMock = stubOrganizations({});
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountView();
    fetchMock.mockImplementation((_input: string, init?: RequestInit) =>
      init?.method === 'POST'
        ? Promise.resolve(
            jsonResponse(
              {
                error: { code: 'duplicate_name', message: 'slug already in use', request_id: 'r' },
              },
              409,
            ),
          )
        : Promise.resolve(jsonResponse(page([acme]))),
    );

    await wrapper.find('#org-name').setValue('Acme');
    await wrapper.find('#org-slug').setValue('acme-engineering');
    await wrapper.find('[data-testid="create-org-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="org-error"]').text()).toContain('slug already in use');
    expect((wrapper.find('#org-name').element as HTMLInputElement).value).toBe('Acme');
    expect(useOrganizationsStore().activeOrganizationId).toBeNull();
  });
});
