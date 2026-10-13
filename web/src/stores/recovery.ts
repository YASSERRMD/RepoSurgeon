import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type {
  FailureClass,
  RecoverRequest,
  RecoveryView,
  RollbackRequest,
  RollbackResponse,
} from '@/api/types';
import { useAuthStore } from './auth';
import { failureOf } from './context';

/**
 * RecoveryFailure is the structured error envelope the recovery view renders.
 * The server's own status and code travel with it: 409 `recovery_loop_detected`
 * (a repair loop is proven and the evidence explains why), 429
 * `recovery_retry_exhausted` (the bounded budget is spent), 404 when the run or
 * the checkpoint does not exist.
 */
export type RecoveryFailure = ReturnType<typeof failureOf>;

/** newEffectId builds the per-action effect identifier the API requires. */
export function newEffectId(): string {
  const random = Math.random().toString(36).slice(2, 10);
  return `effect_${Date.now().toString(36)}_${random}`;
}

/** FAILURE_CLASSES mirrors the contract enum; the server still validates. */
export const FAILURE_CLASSES = [
  'rate_limit',
  'timeout',
  'provider_server',
  'provider_transport',
  'provider_response',
  'tool_error',
  'patch_conflict',
  'patch_rejected',
  'test_failure',
  'credential_expired',
  'lease_lost',
  'worker_lost',
  'sse_interrupted',
  'unknown',
] as const;

/** TERMINAL_STATES are the run states where no further action is offered. */
const TERMINAL_STATES = ['completed', 'canceled'];

/**
 * The recovery store owns the recovery inspector: the journalled timeline, the
 * attempt history, the bounded budgets and the effect receipts of one run, plus
 * the two actions the API offers. Every field is a copy of a server response —
 * a verdict, a budget and a blocked reason are never computed locally, and a
 * refusal keeps whatever the server last answered.
 */
export const useRecoveryStore = defineStore('recovery', () => {
  const client: HealthClient = createHealthClient();
  const runId = ref<string | null>(null);
  const view = ref<RecoveryView | null>(null);
  const recovering = ref(false);
  const rollingBack = ref(false);
  const error = ref<RecoveryFailure | null>(null);
  /** lastRollback is the server's own answer to the most recent restore. */
  const lastRollback = ref<RollbackResponse | null>(null);
  const inFlight = ref(0);

  /** loading follows the requests still outstanding for this inspector. */
  const loading = computed<boolean>(() => inFlight.value > 0);

  const attemptHistory = computed(() => view.value?.attempts ?? []);
  const timeline = computed(() => view.value?.timeline ?? []);
  const budgets = computed(() => view.value?.budgets ?? []);
  const receipts = computed(() => view.value?.receipts ?? []);
  const checkpoints = computed(() => view.value?.checkpoints ?? []);
  const runState = computed<string>(() => view.value?.run_state ?? '');
  const nextAttempt = computed<number>(() => view.value?.next_attempt ?? 1);

  /** blockedReason is the evidence the server journalled when it stopped. */
  const blockedEvent = computed(
    () =>
      timeline.value.find((event) => event.kind === 'loop_detected' || event.kind === 'blocked') ??
      null,
  );

  const isBlocked = computed<boolean>(
    () => blockedEvent.value !== null || runState.value === 'blocked',
  );

  /** canAct is the permission gate only; the API still answers authoritatively. */
  const canAct = computed<boolean>(() => useAuthStore().can('run.execute'));

  const isTerminal = computed<boolean>(() => TERMINAL_STATES.includes(runState.value));

  const exhaustedBudgets = computed(() => budgets.value.filter((budget) => budget.exhausted));

  /** tracked wraps one call so loading follows real requests, never an intent. */
  async function tracked<T>(work: () => Promise<T>): Promise<T> {
    inFlight.value += 1;
    try {
      return await work();
    } finally {
      inFlight.value -= 1;
    }
  }

  function bindRun(next: string | null): void {
    if (runId.value === next) return;
    runId.value = next === '' ? null : next;
    view.value = null;
    error.value = null;
    lastRollback.value = null;
  }

  async function load(next: string): Promise<void> {
    if (next === '') {
      bindRun(null);
      return;
    }
    bindRun(next);
    try {
      const loaded = await tracked(() => client.getRecovery(next));
      if (runId.value !== next) return;
      view.value = loaded;
      error.value = null;
    } catch (cause) {
      if (runId.value !== next) return;
      view.value = null;
      error.value = failureOf(cause);
    }
  }

  /**
   * recover schedules the next bounded attempt. The server stays the authority:
   * a 409 returns the loop evidence and a 429 the spent budget, and both are
   * reported verbatim instead of being smoothed over locally.
   */
  async function recover(request: {
    class?: FailureClass;
    reason: string;
  }): Promise<RecoveryView | null> {
    const current = runId.value;
    if (current === null || request.reason.trim() === '') return null;
    recovering.value = true;
    try {
      await tracked(async () => {
        const body: RecoverRequest = {
          reason: request.reason,
          ...(request.class === undefined ? {} : { class: request.class }),
        };
        await client.recoverRun(current, body, newIdempotencyKey());
      });
      await load(current);
      return view.value;
    } catch (cause) {
      error.value = failureOf(cause);
      return null;
    } finally {
      recovering.value = false;
    }
  }

  /**
   * rollback restores the workspace to the given checkpoint. The effect id is
   * generated here and never reused, so a retried restore answers
   * `duplicate: true` instead of writing the workspace twice.
   */
  async function rollback(request: {
    checkpointId?: string;
    reason: string;
  }): Promise<RecoveryView | null> {
    const current = runId.value;
    if (current === null || request.reason.trim() === '') return null;
    rollingBack.value = true;
    try {
      const restored = await tracked(async () => {
        const body: RollbackRequest = {
          effect_id: newEffectId(),
          reason: request.reason,
          ...(request.checkpointId === undefined ? {} : { checkpoint_id: request.checkpointId }),
        };
        return client.rollbackRun(current, body, newIdempotencyKey());
      });
      lastRollback.value = restored;
      await load(current);
      return view.value;
    } catch (cause) {
      error.value = failureOf(cause);
      return null;
    } finally {
      rollingBack.value = false;
    }
  }

  return {
    runId,
    view,
    error,
    loading,
    recovering,
    rollingBack,
    lastRollback,
    attemptHistory,
    timeline,
    budgets,
    receipts,
    checkpoints,
    runState,
    nextAttempt,
    blockedEvent,
    isBlocked,
    isTerminal,
    canAct,
    exhaustedBudgets,
    bindRun,
    load,
    recover,
    rollback,
  };
});
