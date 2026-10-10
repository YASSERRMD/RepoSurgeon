import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import ProjectDetailView from './ProjectDetailView.vue';
import { useProjectsStore } from '@/stores/projects';
import type { AuditEvent, AuditEventPage, Project, ProjectPage } from '@/api/types';

const project: Project = {
  id: 'prj_billing',
  organization_id: 'org_acme',
  name: 'billing-service',
  description: 'Payments and invoicing',
  version: 3,
  created_at: '2026-10-10T10:00:00Z',
  updated_at: '2026-10-10T11:30:00Z',
};

const auditCreated: AuditEvent = {
  id: 'aud_created',
  organization_id: 'org_acme',
  project_id: project.id,
  actor_id: 'usr_1',
  action: 'project.created',
  entity_type: 'project',
  entity_id: project.id,
  request_id: 'req_1',
  payload: { name: 'billing-service' },
  sequence: 1,
  created_at: '2026-10-10T10:00:00Z',
};

const auditUpdated: AuditEvent = {
  ...auditCreated,
  id: 'aud_updated',
  action: 'project.updated',
  entity_type: 'project',
  payload: { description: 'Payments, invoicing and refunds' },
  sequence: 2,
  created_at: '2026-10-10T10:05:00Z',
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
      { path: '/projects', component: { template: '<div />' } },
      { path: '/projects/:projectId', component: { template: '<div />' } },
    ],
  });
}

/** mountDetail boots one pinia for the view, the stores and the assertions. */
async function mountDetail(): Promise<{ wrapper: ReturnType<typeof mount>; router: Router }> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push(`/projects/${project.id}`);
  await router.isReady();
  const wrapper = mount(ProjectDetailView, {
    global: { plugins: [pinia, router] },
  });
  await flushPromises();
  return { wrapper, router };
}

function stubApi(options: {
  detail?: Project;
  detailStatus?: number;
  audit?: AuditEventPage;
  auditStatus?: number;
  updated?: Project;
  conflictOnPatch?: boolean;
}): Mock {
  return vi.fn((input: string, init?: RequestInit) => {
    const url = String(input);
    if (url.includes('/api/v1/audit')) {
      if (options.auditStatus && options.auditStatus !== 200) {
        return Promise.resolve(
          jsonResponse(
            { error: { code: 'not_found', message: 'project not found', request_id: 'r' } },
            options.auditStatus,
          ),
        );
      }
      if (url.includes('cursor=')) {
        return Promise.resolve(jsonResponse({ items: [auditUpdated], next_cursor: null }));
      }
      return Promise.resolve(
        jsonResponse(
          options.audit ?? { items: [auditUpdated, auditCreated], next_cursor: 'cursor_2' },
        ),
      );
    }
    if (!url.endsWith(project.id)) {
      return Promise.resolve(jsonResponse({ items: [], next_cursor: null } as ProjectPage));
    }
    if (init?.method === 'PATCH') {
      if (options.conflictOnPatch) {
        return Promise.resolve(
          jsonResponse(
            {
              error: {
                code: 'version_mismatch',
                message: 'project was modified by someone else',
                request_id: 'req_9',
                details: { current_version: 7 },
              },
            },
            409,
          ),
        );
      }
      return Promise.resolve(jsonResponse(options.updated ?? { ...project, version: 4 }));
    }
    if (options.detailStatus && options.detailStatus !== 200) {
      return Promise.resolve(
        jsonResponse(
          { error: { code: 'not_found', message: 'resource not found', request_id: 'r' } },
          options.detailStatus,
        ),
      );
    }
    return Promise.resolve(jsonResponse(options.detail ?? project));
  });
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ProjectDetailView', () => {
  it('renders the project header and its audit trail from the API', async () => {
    vi.stubGlobal('fetch', stubApi({}));
    const { wrapper } = await mountDetail();

    expect(wrapper.text()).toContain('billing-service');
    expect(wrapper.text()).toContain(`id ${project.id}`);
    expect(wrapper.text()).toContain('version 3');
    expect(wrapper.text()).toContain(new Date(project.updated_at).toLocaleString());

    const rows = wrapper.findAll('[data-testid^="audit-aud_"]');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('project.updated');
    expect(rows[1].text()).toContain('project.created');
    expect(rows[0].text()).toContain('sequence 2');
    expect(wrapper.find('[data-testid="detail-error"]').exists()).toBe(false);
  });

  it('prefills the description with the server value', async () => {
    vi.stubGlobal('fetch', stubApi({}));
    const { wrapper } = await mountDetail();

    expect((wrapper.find('#detail-description').element as HTMLTextAreaElement).value).toBe(
      'Payments and invoicing',
    );
    expect((wrapper.find('#detail-settings').element as HTMLTextAreaElement).value).toBe('{}');
  });

  it('shows loading skeletons while both requests are in flight', async () => {
    vi.stubGlobal('fetch', stubApi({}));
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = testRouter();
    await router.push(`/projects/${project.id}`);
    await router.isReady();
    const wrapper = mount(ProjectDetailView, { global: { plugins: [pinia, router] } });
    await wrapper.vm.$nextTick();

    // The view renders while the requests are still in flight.
    expect(wrapper.find('[data-testid="detail-loading"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="project-editor"]').exists()).toBe(false);

    await flushPromises();

    expect(wrapper.find('[data-testid="detail-loading"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="project-editor"]').exists()).toBe(true);
  });

  it('shows an error banner when the project cannot be loaded', async () => {
    vi.stubGlobal('fetch', stubApi({ detailStatus: 404 }));
    const { wrapper } = await mountDetail();

    expect(wrapper.find('[data-testid="detail-error"]').text()).toContain('resource not found');
    expect(wrapper.find('[data-testid="project-editor"]').exists()).toBe(false);
  });

  it('saves description and settings with the read version', async () => {
    const fetchMock = stubApi({ updated: { ...project, version: 4 } });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountDetail();

    await wrapper.find('#detail-description').setValue('Payments, invoicing and refunds');
    await wrapper.find('#detail-settings').setValue('{\n  "region": "eu",\n  "risk": "high"\n}');
    await wrapper.find('[data-testid="save-project"]').trigger('click');
    await flushPromises();

    const patchCall = fetchMock.mock.calls.find(
      (call) => (call[1] as RequestInit | undefined)?.method === 'PATCH',
    );
    expect(patchCall).toBeDefined();
    const init = (patchCall?.[1] ?? {}) as RequestInit;
    expect(String(patchCall?.[0])).toContain('/api/v1/projects/prj_billing');
    expect(JSON.parse(String(init.body))).toEqual({
      version: 3,
      description: 'Payments, invoicing and refunds',
      settings: { region: 'eu', risk: 'high' },
    });
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^idem_/);

    // The server response wins: the editor shows the version the API returned.
    expect(wrapper.text()).toContain('version 4');
  });

  it('refuses to save invalid settings JSON', async () => {
    const fetchMock = stubApi({});
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountDetail();
    const callsBefore = fetchMock.mock.calls.length;

    await wrapper.find('#detail-settings').setValue('{ not json');
    await wrapper.find('[data-testid="save-project"]').trigger('click');

    expect(fetchMock.mock.calls.length).toBe(callsBefore);
    expect(wrapper.find('[data-testid="detail-settings-error"]').text()).toContain(
      'Settings must be valid JSON',
    );

    await wrapper.find('#detail-settings').setValue('["array"]');
    await wrapper.find('[data-testid="save-project"]').trigger('click');
    expect(fetchMock.mock.calls.length).toBe(callsBefore);
    expect(wrapper.find('[data-testid="detail-settings-error"]').text()).toContain(
      'Settings must be a JSON object',
    );
  });

  it('explains a 409 conflict and reloads the latest version on demand', async () => {
    const fetchMock = stubApi({ conflictOnPatch: true });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountDetail();
    fetchMock.mockClear();

    await wrapper.find('#detail-description').setValue('stale edit');
    await wrapper.find('[data-testid="save-project"]').trigger('click');
    await flushPromises();

    const banner = wrapper.find('[data-testid="conflict-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain(
      'This project was modified elsewhere — reload to see the latest version',
    );
    expect(banner.text()).toContain('Server is now at version 7');
    // The stale edit is not silently discarded nor pushed onto the server.
    expect(wrapper.text()).toContain('version 3');

    fetchMock.mockImplementation((input: string, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/api/v1/audit')) {
        return Promise.resolve(jsonResponse({ items: [auditCreated], next_cursor: null }));
      }
      if (init?.method === 'PATCH') {
        return Promise.resolve(jsonResponse({ ...project, version: 7 }));
      }
      return Promise.resolve(jsonResponse({ ...project, version: 7 }));
    });

    await wrapper.find('[data-testid="conflict-reload"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="conflict-banner"]').exists()).toBe(false);
    expect(wrapper.text()).toContain('version 7');
    expect(useProjectsStore().conflict).toBeNull();
  });

  it('shows a rejected save banner for non-conflict failures', async () => {
    const fetchMock = stubApi({});
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountDetail();
    fetchMock.mockImplementation((input: string, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/api/v1/audit')) {
        return Promise.resolve(jsonResponse({ items: [], next_cursor: null }));
      }
      if (init?.method === 'PATCH') {
        return Promise.resolve(
          jsonResponse(
            { error: { code: 'permission_denied', message: 'viewer role', request_id: 'r' } },
            403,
          ),
        );
      }
      return Promise.resolve(jsonResponse(project));
    });

    await wrapper.find('[data-testid="save-project"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="save-error"]').text()).toContain('viewer role');
    expect(wrapper.find('[data-testid="conflict-banner"]').exists()).toBe(false);
  });

  it('filters the audit trail by action and pages older events', async () => {
    const fetchMock = stubApi({
      audit: { items: [auditCreated], next_cursor: 'cursor_2' },
    });
    vi.stubGlobal('fetch', fetchMock);
    const { wrapper } = await mountDetail();

    await wrapper.find('[data-testid="audit-filter"]').setValue('project.created');
    await wrapper.find('[data-testid="audit-filter-submit"]').trigger('submit');
    await flushPromises();

    const auditCall = fetchMock.mock.calls.find((call) =>
      String(call[0]).includes('/api/v1/audit'),
    );
    expect(String(auditCall?.[0])).toBe(`/api/v1/audit?project_id=${project.id}`);
    const filteredCall = fetchMock.mock.calls.filter((call) =>
      String(call[0]).includes('/api/v1/audit'),
    );
    expect(String(filteredCall[1]?.[0])).toBe(
      `/api/v1/audit?project_id=${project.id}&action=project.created`,
    );

    await wrapper.find('[data-testid="audit-load-more"]').trigger('click');
    await flushPromises();

    const paged = fetchMock.mock.calls.filter((call) => String(call[0]).includes('/api/v1/audit'));
    expect(String(paged[2]?.[0])).toBe(
      `/api/v1/audit?project_id=${project.id}&action=project.created&cursor=cursor_2`,
    );
    expect(wrapper.findAll('[data-testid^="audit-aud_"]')).toHaveLength(2);
    expect(wrapper.find('[data-testid="audit-load-more"]').exists()).toBe(false);
  });

  it('shows an empty audit state and an audit error state', async () => {
    vi.stubGlobal('fetch', stubApi({ audit: { items: [], next_cursor: null } }));
    const emptyWrapper = (await mountDetail()).wrapper;
    const empty = emptyWrapper.find('[data-testid="empty-audit"]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('No audit events yet');

    vi.stubGlobal('fetch', stubApi({ auditStatus: 404 }));
    const failed = (await mountDetail()).wrapper;
    const banner = failed.find('[data-testid="audit-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('project not found');
    expect(failed.find('[data-testid="audit-list"]').exists()).toBe(false);
  });
});
