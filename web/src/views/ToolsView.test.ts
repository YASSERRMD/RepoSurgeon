import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import ToolsView from './ToolsView.vue';
import type { ToolDescriptor } from '@/api/types';

const catalog: ToolDescriptor[] = [
  {
    name: 'read_file',
    description: 'Read a file inside the workspace',
    risk: 'read_only',
    permission: 'project.read',
    approval: false,
    sandboxed: false,
    permitted: true,
  },
  {
    name: 'write_patch',
    description: 'Apply a unified diff inside the workspace',
    risk: 'workspace_write',
    permission: 'project.write',
    approval: false,
    sandboxed: false,
    permitted: true,
  },
  {
    name: 'run_tests',
    description: 'Run the project test suite in the isolated runner',
    risk: 'sandboxed_exec',
    permission: 'run.execute',
    approval: false,
    sandboxed: true,
    permitted: true,
  },
  {
    name: 'merge_pr',
    description: 'Merge a reviewed pull request on GitHub',
    risk: 'external_side_effect',
    permission: 'merge.execute',
    approval: true,
    sandboxed: false,
    permitted: false,
  },
];

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(code: string, message: string, status: number): Response {
  return jsonResponse({ error: { code, message, request_id: 'req_tools_view_test' } }, status);
}

function testRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/tools', component: ToolsView }],
  });
}

async function mountTools(): Promise<ReturnType<typeof mount>> {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = testRouter();
  await router.push('/tools');
  await router.isReady();
  const wrapper = mount(ToolsView, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return wrapper;
}

function stubCatalog(options: { items?: ToolDescriptor[]; fail?: boolean } = {}): Mock {
  return vi.fn((input: string): Promise<Response> => {
    const url = String(input);
    if (!url.includes('/api/v1/tools')) {
      return Promise.reject(new Error(`unexpected request to ${url}`));
    }
    return options.fail === true
      ? Promise.resolve(
          errorResponse('permission_denied', 'you do not have access to this organization', 403),
        )
      : Promise.resolve(jsonResponse({ items: options.items ?? catalog }));
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

describe('ToolsView', () => {
  it('renders every catalog tool with its risk badge and permission', async () => {
    vi.stubGlobal('fetch', stubCatalog());
    const wrapper = await mountTools();

    const rows = wrapper.findAll('tr[data-testid^="tool-"]');
    expect(rows).toHaveLength(4);

    const readOnly = wrapper.find('[data-testid="tool-read_file"]');
    expect(readOnly.text()).toContain('read_file');
    expect(readOnly.text()).toContain('Read a file inside the workspace');
    expect(readOnly.find('[data-testid="tool-risk"]').text()).toBe('read only');
    expect(readOnly.find('[data-testid="tool-risk"]').classes()).toContain('risk-read_only');
    expect(readOnly.text()).toContain('project.read');
    expect(readOnly.find('[data-testid="tool-permitted-read_file"]').text()).toBe(
      'permitted for your role',
    );
    expect(readOnly.find('[data-testid="tool-approval-required"]').exists()).toBe(false);
    expect(readOnly.find('[data-testid="tool-sandboxed"]').exists()).toBe(false);

    const workspaceWrite = wrapper.find('[data-testid="tool-write_patch"]');
    expect(workspaceWrite.find('[data-testid="tool-risk"]').classes()).toContain(
      'risk-workspace_write',
    );
    expect(workspaceWrite.find('[data-testid="tool-risk"]').text()).toBe('workspace write');
    expect(workspaceWrite.text()).toContain('project.write');

    const sandboxedExec = wrapper.find('[data-testid="tool-run_tests"]');
    expect(sandboxedExec.find('[data-testid="tool-risk"]').classes()).toContain(
      'risk-sandboxed_exec',
    );
    expect(sandboxedExec.find('[data-testid="tool-risk"]').text()).toBe('sandboxed exec');
    expect(sandboxedExec.find('[data-testid="tool-sandboxed"]').text()).toBe('sandboxed');
    expect(sandboxedExec.find('[data-testid="tool-permitted-run_tests"]').text()).toBe(
      'permitted for your role',
    );

    const sideEffect = wrapper.find('[data-testid="tool-merge_pr"]');
    expect(sideEffect.find('[data-testid="tool-risk"]').classes()).toContain(
      'risk-external_side_effect',
    );
    expect(sideEffect.find('[data-testid="tool-risk"]').text()).toBe('external side effect');
    expect(sideEffect.find('[data-testid="tool-approval-required"]').text()).toBe(
      'approval required',
    );
    expect(sideEffect.find('[data-testid="tool-permitted-merge_pr"]').text()).toBe(
      'not permitted for your role',
    );
    expect(sideEffect.text()).toContain('merge.execute');
  });

  it('explains that only catalog tools exist and model output is never executed', async () => {
    vi.stubGlobal('fetch', stubCatalog());
    const wrapper = await mountTools();

    const note = wrapper.find('[data-testid="tools-note"]');
    expect(note.exists()).toBe(true);
    expect(note.text()).toContain('outside this catalog');
    expect(note.text()).toContain('Model-supplied commands are never executed directly');
  });

  it('shows an empty state when the catalog has no tools', async () => {
    vi.stubGlobal('fetch', stubCatalog({ items: [] }));
    const wrapper = await mountTools();

    expect(wrapper.find('[data-testid="empty-tools"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="tools-list"]').exists()).toBe(false);
  });

  it('reports a 403 failure in a banner and reloads on retry', async () => {
    let failing = true;
    const fetchMock = vi.fn((input: string): Promise<Response> => {
      const url = String(input);
      if (!url.includes('/api/v1/tools')) {
        return Promise.reject(new Error(`unexpected request to ${url}`));
      }
      if (failing) {
        return Promise.resolve(
          errorResponse('permission_denied', 'you do not have access to this organization', 403),
        );
      }
      return Promise.resolve(jsonResponse({ items: catalog }));
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = await mountTools();

    const banner = wrapper.find('[data-testid="tools-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('you do not have access to this organization');
    expect(wrapper.find('[data-testid="tools-list"]').exists()).toBe(false);

    failing = false;
    await wrapper.find('[data-testid="tools-retry"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="tools-error"]').exists()).toBe(false);
    expect(wrapper.findAll('tr[data-testid^="tool-"]')).toHaveLength(4);
  });
});
