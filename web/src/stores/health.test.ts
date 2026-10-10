import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

import type { ReadinessReport, SystemInfo } from '@/api/types';
import { useHealthStore } from './health';

const system: SystemInfo = {
  name: 'reposurgeon',
  version: '0.1.0',
  git_commit: 'abc123',
  build_time: '2026-10-10T10:00:00Z',
  environment: 'development',
  go_version: 'go1.25.5',
  started_at: '2026-10-10T09:59:00Z',
  uptime_seconds: 60,
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
  checks: [{ name: 'database', status: 'down', detail: 'database unreachable', duration_ms: 5 }],
  generated_at: '2026-10-10T10:00:00Z',
};

function stubFetch(responses: {
  live?: unknown;
  ready?: unknown;
  system?: unknown;
  reject?: boolean;
}): void {
  const live = vi.fn().mockImplementation(() => {
    if (responses.reject) return Promise.reject(new Error('ECONNREFUSED'));
    return Promise.resolve(
      new Response(JSON.stringify(responses.live ?? { status: 'ok' }), { status: 200 }),
    );
  });
  const readyFn = vi.fn().mockImplementation(() => {
    if (responses.reject) return Promise.reject(new Error('ECONNREFUSED'));
    const status =
      (responses.ready as ReadinessReport | undefined)?.status === 'unavailable' ? 503 : 200;
    return Promise.resolve(new Response(JSON.stringify(responses.ready ?? ready), { status }));
  });
  const systemFn = vi.fn().mockImplementation(() => {
    if (responses.reject) return Promise.reject(new Error('ECONNREFUSED'));
    return Promise.resolve(
      new Response(JSON.stringify(responses.system ?? system), { status: 200 }),
    );
  });
  const fetchMock = vi.fn().mockImplementation((input: string): Promise<Response> => {
    if (input.includes('/health/live')) return live() as Promise<Response>;
    if (input.includes('/health/ready')) return readyFn() as Promise<Response>;
    return systemFn() as Promise<Response>;
  });
  vi.stubGlobal('fetch', fetchMock);
}

beforeEach(() => {
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('health store', () => {
  it('records a ready report from real probe responses', async () => {
    stubFetch({});
    const store = useHealthStore();

    await store.refresh();

    expect(store.status).toBe('ready');
    expect(store.readiness?.checks).toHaveLength(2);
    expect(store.system?.version).toBe('0.1.0');
    expect(store.error).toBeNull();
    expect(store.lastCheckedAt).not.toBeNull();
    expect(store.failingChecks).toHaveLength(0);
  });

  it('reports unavailable with failing check detail', async () => {
    stubFetch({ ready: unavailable });
    const store = useHealthStore();

    await store.refresh();

    expect(store.status).toBe('unavailable');
    expect(store.failingChecks).toHaveLength(1);
    expect(store.failingChecks[0].detail).toContain('unreachable');
    expect(store.pendingChecks).toHaveLength(0);
  });

  it('reports an error state when the API cannot be reached', async () => {
    stubFetch({ reject: true });
    const store = useHealthStore();

    await store.refresh();

    expect(store.status).toBe('error');
    expect(store.error).toContain('ECONNREFUSED');
    expect(store.readiness).toBeNull();
  });

  it('keeps serving readiness even when only system info fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation((input: string) => {
        if (input.includes('/api/v1/system/info')) {
          return Promise.resolve(
            new Response('{}', { status: 500, headers: { 'Content-Type': 'application/json' } }),
          );
        }
        if (input.includes('/health/ready')) {
          return Promise.resolve(new Response(JSON.stringify(ready), { status: 200 }));
        }
        return Promise.resolve(new Response(JSON.stringify({ status: 'ok' }), { status: 200 }));
      }),
    );
    const store = useHealthStore();

    await store.refresh();

    expect(store.status).toBe('ready');
    expect(store.system).toBeNull();
    expect(store.error).not.toBeNull();
  });

  it('polls on an interval and stops cleanly', async () => {
    vi.useFakeTimers();
    stubFetch({});
    const store = useHealthStore();

    store.startPolling(1000);
    expect(store.pollTimer).not.toBeNull();

    const fetchSpy = globalThis.fetch as unknown as Mock;
    const callsAfterStart = fetchSpy.mock.calls.length;
    await vi.advanceTimersByTimeAsync(1000);
    const callsAfterTick = fetchSpy.mock.calls.length;
    expect(callsAfterTick).toBeGreaterThan(callsAfterStart);

    store.stopPolling();
    expect(store.pollTimer).toBeNull();
    const callsAfterStop = fetchSpy.mock.calls.length;
    await vi.advanceTimersByTimeAsync(5000);
    expect(fetchSpy.mock.calls.length).toBe(callsAfterStop);
  });

  it('does not start polling twice', () => {
    const store = useHealthStore();
    const setIntervalSpy = vi.spyOn(window, 'setInterval');
    store.startPolling(1000);
    store.startPolling(1000);
    expect(setIntervalSpy).toHaveBeenCalledOnce();
    store.stopPolling();
    setIntervalSpy.mockRestore();
  });
});
