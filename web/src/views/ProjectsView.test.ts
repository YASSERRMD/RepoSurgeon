import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import ProjectsView from './ProjectsView.vue';
import type { OrganizationPage, Project, ProjectPage } from '@/api/types';

const organizationId = 'org_acme';

const billing: Project = {
  id: 'prj_billing',
  organization_id: organizationId,
  name: 'billing-service',
  description: 'Payments and invoicing',
  version: 3,
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T11:30:00Z',
};

const indexing: Project = {
  ...billing,
  id: 'prj_index',
  name: 'indexing',
  description: 'Search indexing',
  version: 1,
  updated_at: '2026-10-10T09:00:00Z',
};

const organizationsPage: OrganizationPage = {
  items: [
    {
      id: organizationId,
      slug: 'acme-engineering',
      name: 'Acme Engineering',
      role: 'owner',
      created_at: '2026-10-10T10:00:00Z',
      updated_at: '2026-10-10T10:00:00Z',
    },
  ],
  next_cursor: null,
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/organizations', component: { template: '<div />' } },
      { path: '/projects', component: { template: '<div />' } },
      { path: '/projects/:projectId', component: { template: '<div />' } },
    ],
  });
}

/** mountProjects boots one pinia for the view, the stores and the assertions. */
async function mountProjects(options: { activeOrganization?: string | null } = {}): Promise<{
  wrapper: ReturnType<typeof mount>;
  router: Router;
}> {
  if (options.activeOrganization === undefined) {
    window.localStorage.setItem('reposurgeon.activeOrganization', organizationId);
  } else if (options.activeOrganization === null) {
    window.localStorage.removeItem('reposurgeon.activeOrganization');
  } else {
    window.localStorage.setItem('reposurgeon.activeOrganization', options.activeOrganization);
  }
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push('/projects');
  await router.isReady();
  const wrapper = mount(ProjectsView, {
    global: { plugins: [pinia, router] },
  });
  await flushPromises();
  return { wrapper, router };
}

/** stubApi serves organizations and projects pages; see options for behaviours. */
function stubApi(options: {
  projects?: ProjectPage;
  failProjects?: boolean | 'http';
  created?: Project;
  nextPages?: ProjectPage[];
}): Mock {
  return vi.fn((input: string, init?: RequestInit) => {
    const url = String(input);
    if (url.includes('/api/v1/organizations')) {
      return Promise.resolve(jsonResponse(organizationsPage));
    }
    if (url.includes('/api/v1/projects')) {
      if (init?.method === 'POST') {
        return Promise.resolve(jsonResponse(options.created ?? indexing, 201));
      }
      if (options.failProjects) {
        return options.failProjects === 'http'
          ? Promise.resolve(
              jsonResponse(
                { error: { code: 'permission_denied', message: 'no access', request_id: 'r' } },
                403,
              ),
            )
          : Promise.reject(new Error('ECONNREFUSED'));
      }
      const extra = options.nextPages?.length;
      const body =
        extra !== undefined && extra > 0
          ? (options.nextPages?.[Math.max(0, extra - 1)] ?? options.projects)
          : options.projects;
      return Promise.resolve(jsonResponse(body ?? { items: [billing], next_cursor: null }));
    }
    return Promise.resolve(jsonResponse({ status: 'ok' }));
  });
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('ProjectsView', () => {
  it('asks for an organization when none is active', async () => {
    vi.stubGlobal('fetch', stubApi({}));
    const { wrapper } = await mountProjects({ activeOrganization: null });

    const empty = wrapper.find('[data-testid="no-active-organization"]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('organization');
    expect(wrapper.find('[data-testid="project-grid"]').exists()).toBe(false);
    expect(wrapper.find('a[href="/organizations"]').exists()).toBe(true);
  });

  it('renders project cards from the API for the active organization', async () => {
    vi.stubGlobal(
      'fetch',
      stubApi({ projects: { items: [billing, indexing], next_cursor: null } }),
    );
    const { wrapper } = await mountProjects();

    expect(wrapper.text()).toContain('Acme Engineering');
    const cards = wrapper.findAll('[data-testid^="project-prj_"]');
    expect(cards).toHaveLength(2);
    expect(cards[0].text()).toContain('billing-service');
    expect(cards[0].text()).toContain('Payments and invoicing');
    expect(cards[0].text()).toContain('v3');
    expect(cards[0].text()).toContain(new Date(billing.updated_at).toLocaleString());
  });

  it('shows an empty state when the organization has no projects', async () => {
    vi.stubGlobal('fetch', stubApi({ projects: { items: [], next_cursor: null } }));
    const { wrapper } = await mountProjects();

    const empty = wrapper.find('[data-testid="empty-projects"]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('No projects yet');
    expect(wrapper.find('[data-testid="project-grid"]').exists()).toBe(false);
  });

  it('shows an error banner with retry on failure', async () => {
    const fetchMock = stubApi({ failProjects: true });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountProjects();

    const banner = wrapper.find('[data-testid="projects-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('ECONNREFUSED');

    fetchMock.mockImplementation((input: string) =>
      Promise.resolve(
        jsonResponse(
          String(input).includes('/api/v1/organizations')
            ? organizationsPage
            : { items: [billing], next_cursor: null },
        ),
      ),
    );
    await wrapper.find('[data-testid="projects-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="projects-error"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-testid^="project-prj_"]')).toHaveLength(1);
  });

  it('surfaces a 403 from the API', async () => {
    vi.stubGlobal('fetch', stubApi({ failProjects: 'http' }));
    const { wrapper } = await mountProjects();

    expect(wrapper.find('[data-testid="projects-error"]').text()).toContain('no access');
  });

  it('sends the search query to the API', async () => {
    const fetchMock = stubApi({ projects: { items: [indexing], next_cursor: null } });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountProjects();
    fetchMock.mockClear();

    await wrapper.find('[data-testid="project-search"]').setValue('index');
    await wrapper.find('[data-testid="project-search-submit"]').trigger('submit');
    await flushPromises();

    const projectCall = fetchMock.mock.calls.find((call) =>
      String(call[0]).includes('/api/v1/projects'),
    );
    expect(String(projectCall?.[0])).toBe(
      `/api/v1/projects?organization_id=${organizationId}&q=index`,
    );
    expect(wrapper.findAll('[data-testid^="project-prj_"]')).toHaveLength(1);
  });

  it('creates a project and navigates to its detail page', async () => {
    const fetchMock = stubApi({ projects: { items: [billing], next_cursor: null } });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper, router } = await mountProjects();

    await wrapper.find('[data-testid="new-project-button"]').trigger('click');
    await wrapper.find('#project-name').setValue('indexing');
    await wrapper.find('#project-description').setValue('Search indexing');
    await wrapper.find('[data-testid="create-project-submit"]').trigger('submit');
    await flushPromises();

    const postCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'POST',
    );
    expect(postCall).toBeDefined();
    const init = (postCall?.[1] ?? {}) as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({
      organization_id: organizationId,
      name: 'indexing',
      description: 'Search indexing',
    });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^idem_/);
    expect(router.currentRoute.value.fullPath).toBe('/projects/prj_index');
  });

  it('keeps the form open and shows the error when creation is rejected', async () => {
    const fetchMock = stubApi({ projects: { items: [billing], next_cursor: null } });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper, router } = await mountProjects();
    fetchMock.mockImplementation((input: string, init?: RequestInit) =>
      init?.method === 'POST'
        ? Promise.resolve(
            jsonResponse(
              { error: { code: 'permission_denied', message: 'no access', request_id: 'r' } },
              403,
            ),
          )
        : Promise.resolve(
            jsonResponse(
              String(input).includes('/api/v1/organizations')
                ? organizationsPage
                : { items: [billing], next_cursor: null },
            ),
          ),
    );

    await wrapper.find('[data-testid="new-project-button"]').trigger('click');
    await wrapper.find('#project-name').setValue('indexing');
    await wrapper.find('[data-testid="create-project-submit"]').trigger('submit');
    await flushPromises();

    expect(wrapper.find('[data-testid="projects-error"]').text()).toContain('no access');
    expect(wrapper.find('[data-testid="create-project-form"]').exists()).toBe(true);
    expect(router.currentRoute.value.fullPath).toBe('/projects');
  });

  it('requires a project name before calling the API', async () => {
    const fetchMock = stubApi({ projects: { items: [billing], next_cursor: null } });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountProjects();
    fetchMock.mockClear();

    await wrapper.find('[data-testid="new-project-button"]').trigger('click');
    await wrapper.find('#project-name').setValue(' ');
    await wrapper.find('[data-testid="create-project-submit"]').trigger('submit');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="project-name-error"]').text()).toContain('Name is required');
  });

  it('loads the next page when a cursor is returned', async () => {
    const fetchMock = stubApi({
      projects: { items: [billing], next_cursor: 'cursor_2' },
      nextPages: [{ items: [indexing], next_cursor: 'cursor_3' }],
    });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountProjects();

    expect(wrapper.find('[data-testid="projects-load-more"]').exists()).toBe(true);
    await wrapper.find('[data-testid="projects-load-more"]').trigger('click');
    await flushPromises();

    expect(wrapper.findAll('[data-testid^="project-prj_"]')).toHaveLength(2);
    expect(wrapper.find('[data-testid="projects-load-more"]').exists()).toBe(true);

    fetchMock.mockImplementation((input: string) =>
      Promise.resolve(
        jsonResponse(String(input).includes('/api/v1/organizations') ? organizationsPage : null),
      ),
    );
    fetchMock.mockImplementation((input: string) =>
      Promise.resolve(
        jsonResponse(
          String(input).includes('/api/v1/organizations')
            ? organizationsPage
            : { items: [], next_cursor: null },
        ),
      ),
    );
    await wrapper.find('[data-testid="projects-load-more"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="projects-load-more"]').exists()).toBe(false);
  });

  it('navigates to the project detail when a card is opened', async () => {
    vi.stubGlobal('fetch', stubApi({ projects: { items: [billing], next_cursor: null } }));
    const { wrapper, router } = await mountProjects();

    await wrapper.find('[data-testid="project-prj_billing"] button').trigger('click');
    await flushPromises();

    expect(router.currentRoute.value.fullPath).toBe('/projects/prj_billing');
  });
});
