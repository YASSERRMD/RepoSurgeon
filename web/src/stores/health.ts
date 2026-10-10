import { defineStore } from 'pinia';

import { ApiError, createHealthClient, type HealthClient } from '@/api/client';
import type { ReadinessCheck, ReadinessReport, SystemInfo } from '@/api/types';

export type OperationalStatus = 'idle' | 'loading' | 'ready' | 'unavailable' | 'error';

export interface HealthState {
  client: HealthClient;
  status: OperationalStatus;
  live: { ok: boolean; error: string };
  readiness: ReadinessReport | null;
  system: SystemInfo | null;
  error: string | null;
  lastCheckedAt: string | null;
  pollTimer: number | null;
}

const POLL_INTERVAL_MS = 10_000;

export const useHealthStore = defineStore('health', {
  state: (): HealthState => ({
    client: createHealthClient(),
    status: 'idle',
    live: { ok: false, error: '' },
    readiness: null,
    system: null,
    error: null,
    lastCheckedAt: null,
    pollTimer: null,
  }),

  getters: {
    isReady: (state): boolean => state.status === 'ready',
    failingChecks(state): ReadinessCheck[] {
      if (!state.readiness) return [];
      return state.readiness.checks.filter((check) => check.status === 'down');
    },
    pendingChecks(state): ReadinessCheck[] {
      if (!state.readiness) return [];
      return state.readiness.checks.filter((check) => check.status !== 'down');
    },
  },

  actions: {
    /** refresh probes liveness, readiness and system info and stores real results. */
    async refresh(): Promise<void> {
      this.status =
        this.status === 'ready' || this.status === 'unavailable' ? this.status : 'loading';

      const [liveResult, readinessResult, systemResult] = await Promise.allSettled([
        this.client.getLive(),
        this.client.getReady(),
        this.client.getSystemInfo(),
      ]);

      if (liveResult.status === 'fulfilled') {
        this.live = { ok: liveResult.value.status === 'ok', error: '' };
      } else {
        this.live = { ok: false, error: errorMessage(liveResult.reason) };
      }

      if (readinessResult.status === 'fulfilled') {
        this.readiness = readinessResult.value;
      } else {
        this.readiness = null;
      }

      if (systemResult.status === 'fulfilled') {
        this.system = systemResult.value;
      } else {
        this.system = null;
      }

      const failures = [liveResult, readinessResult, systemResult].filter(
        (result) => result.status === 'rejected',
      );
      this.error = failures.length > 0 ? errorMessage(failures[0].reason) : null;
      this.lastCheckedAt = new Date().toISOString();

      if (readinessResult.status === 'fulfilled') {
        this.status = readinessResult.value.status === 'ready' ? 'ready' : 'unavailable';
      } else if (systemResult.status === 'fulfilled' && liveResult.status === 'fulfilled') {
        this.status = 'unavailable';
      } else {
        this.status = 'error';
      }
    },

    /** startPolling refreshes on an interval and is safe to call repeatedly. */
    startPolling(intervalMs: number = POLL_INTERVAL_MS): void {
      if (this.pollTimer !== null) return;
      this.pollTimer = window.setInterval(() => {
        void this.refresh();
      }, intervalMs);
    },

    stopPolling(): void {
      if (this.pollTimer === null) return;
      window.clearInterval(this.pollTimer);
      this.pollTimer = null;
    },
  },
});

function errorMessage(reason: unknown): string {
  if (reason instanceof ApiError) return reason.message;
  if (reason instanceof Error) return reason.message;
  return String(reason);
}
