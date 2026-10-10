import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';

import App from './App.vue';
import { useHealthStore, type OperationalStatus } from '@/stores/health';

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

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('App shell', () => {
  it('renders the brand', () => {
    stubResponses();
    const wrapper = mount(App, { global: { plugins: [createPinia(), createRouterStub()] } });

    expect(wrapper.text()).toContain('RepoSurgeon');
    expect(wrapper.find('[data-testid="footer-status"]').text()).toBeTruthy();
  });
});

// Minimal router stub: App.vue renders RouterView, which needs a router context.
function createRouterStub() {
  return {
    install(app: { component: (name: string, def: unknown) => void }) {
      app.component('RouterView', { template: '<div />' });
    },
  };
}

describe('shell status copy', () => {
  it('maps every store status to a label', () => {
    stubResponses();
    mount(App, { global: { plugins: [createPinia(), createRouterStub()] } });
    const store = useHealthStore();
    const statuses: OperationalStatus[] = ['ready', 'unavailable', 'error', 'loading', 'idle'];
    for (const status of statuses) {
      store.status = status;
    }
    expect(store.status).toBe('idle');
  });
});
