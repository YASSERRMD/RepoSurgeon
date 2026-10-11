import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type {
  PlanDecision,
  PlanDecisionChoice,
  PlanFinding,
  PlanFindingSeverity,
  PlanNode,
  PlanSubmission,
  PlanView,
} from '@/api/types';
import { useAuthStore } from './auth';
import { failureOf } from './context';

/**
 * PlanFailure is the structured error envelope the plan inspector renders. The
 * server's own status and code travel with it: 404 `plan_not_found` (no plan
 * for this run yet), 409 `plan_conflict` (the revision was already decided or
 * superseded) and 422 `plan_verification_failed` (a referenced path is not in
 * the indexed workspace).
 */
export type PlanFailure = ReturnType<typeof failureOf>;

function unboundRun(): PlanFailure {
  return {
    status: 0,
    code: 'plan_run_unbound',
    message: 'no run is selected, so there is no plan revision to read',
    requestId: '',
    retryable: false,
  };
}

function undecidable(): PlanFailure {
  return {
    status: 0,
    code: 'plan_revision_unavailable',
    message: 'no plan revision is loaded, so there is no revision to decide',
    requestId: '',
    retryable: false,
  };
}

/**
 * orderByPrerequisites returns the plan nodes in dependency order: a node only
 * appears after every node it names in `prerequisites`. The order is Kahn's
 * algorithm over the server's own list, so the sequence the reviewer sees is
 * derived from the plan and never from an ordering the client invented.
 * A cycle (which the server's validation rejects) leaves the remaining nodes
 * in their original order rather than dropping them from the screen.
 */
export function orderByPrerequisites(nodes: PlanNode[]): PlanNode[] {
  const remaining = [...nodes];
  const ordered: PlanNode[] = [];
  const placed = new Set<string>();

  let progress = true;
  while (remaining.length > 0 && progress) {
    progress = false;
    for (let index = 0; index < remaining.length;) {
      const node = remaining[index];
      if (node === undefined) continue;
      const ready = node.prerequisites?.every((key) => placed.has(key)) ?? true;
      if (!ready) {
        index += 1;
        continue;
      }
      ordered.push(node);
      placed.add(node.key);
      remaining.splice(index, 1);
      progress = true;
    }
  }

  // Anything left is part of a cycle; keep it visible in the submitted order.
  return [...ordered, ...remaining];
}

/**
 * The plan store owns the plan inspector: the latest revision for one run, the
 * findings verification produced, and the human decision recorded against a
 * revision. Every field is a copy of a server response — the status of a
 * revision, its findings and its approvals are never computed locally, and a
 * refusal keeps whatever the server last answered.
 */
export const usePlanStore = defineStore('plan', () => {
  const client: HealthClient = createHealthClient();
  const runId = ref<string | null>(null);
  const plan = ref<PlanView | null>(null);
  const deciding = ref(false);
  const error = ref<PlanFailure | null>(null);
  const inFlight = ref(0);

  /** loading follows the requests still outstanding for this inspector. */
  const loading = computed<boolean>(() => inFlight.value > 0);

  /** isAwaitingApproval reports the server status; only the server sets it. */
  const isAwaitingApproval = computed<boolean>(() => plan.value?.status === 'awaiting_approval');

  const isApproved = computed<boolean>(() => plan.value?.status === 'approved');

  function findingsOf(severity: PlanFindingSeverity): PlanFinding[] {
    return plan.value?.findings.filter((finding) => finding.severity === severity) ?? [];
  }

  /** blockingFindings are the errors that stop a plan from being actionable. */
  const blockingFindings = computed<PlanFinding[]>(() => findingsOf('error'));

  const warnings = computed<PlanFinding[]>(() => findingsOf('warning'));

  /**
   * canDecide is the permission gate only. Whether a revision can actually be
   * decided right now is `isAwaitingApproval`, which comes from the server.
   */
  const canDecide = computed<boolean>(() => useAuthStore().can('approval.decide'));

  /** orderedNodes lists the DAG in dependency order for the inspector. */
  const orderedNodes = computed<PlanNode[]>(() => orderByPrerequisites(plan.value?.nodes ?? []));

  /** tracked wraps one call so loading follows real requests, never an intent. */
  async function tracked<T>(work: () => Promise<T>): Promise<T> {
    inFlight.value += 1;
    try {
      return await work();
    } finally {
      inFlight.value -= 1;
    }
  }

  /** bindRun selects the run whose plan revision is inspected. */
  function bindRun(id: string | null): void {
    runId.value = id === '' ? null : id;
    plan.value = null;
    error.value = null;
  }

  /** reset drops the revision and the failure; a new run starts clean. */
  function reset(): void {
    bindRun(null);
  }

  /**
   * load reads the latest revision for a run. A 404 (no plan yet) is reported
   * with the server code and leaves no plan on screen, so the empty state is
   * the answer rather than a fabricated revision. Reloading the same run keeps
   * the revision on screen until the server answers again.
   */
  async function load(id: string): Promise<PlanView | null> {
    if (runId.value !== id) bindRun(id);
    try {
      const loaded = await tracked(() => client.getPlan(id));
      // A response that arrives after the operator moved to another run is
      // dropped instead of being displayed under the wrong run.
      if (runId.value !== id) return null;
      plan.value = loaded;
      error.value = null;
      return loaded;
    } catch (cause) {
      if (runId.value !== id) return null;
      // The revision on screen stays: it is the last server answer, and a
      // refused read must not blank out what the reviewer was looking at.
      error.value = failureOf(cause);
      return null;
    }
  }

  /**
   * save stores a revision and adopts the server's projection verbatim: the
   * revision number, status and findings in the response are what the UI shows.
   * A 409 or a 422 refusal keeps the previous revision and surfaces the code.
   */
  async function save(body: PlanSubmission): Promise<PlanView | null> {
    const id = runId.value;
    if (id === null) {
      error.value = unboundRun();
      return null;
    }
    try {
      const stored = await tracked(() => client.savePlan(id, body, newIdempotencyKey()));
      if (runId.value !== id) return null;
      plan.value = stored;
      return stored;
    } catch (cause) {
      if (runId.value !== id) return null;
      error.value = failureOf(cause);
      return null;
    }
  }

  /**
   * decide records the human decision for the revision the server last
   * reported. The revision is taken from that response, never typed by the
   * caller, and the resulting status comes from the server's answer. A 409
   * (`plan_conflict`) means the revision was already decided or superseded;
   * the previous revision stays on screen with the server's explanation.
   */
  async function decide(decision: PlanDecisionChoice, reason = ''): Promise<PlanView | null> {
    const id = runId.value;
    const current = plan.value;
    if (id === null) {
      error.value = unboundRun();
      return null;
    }
    if (current === null) {
      error.value = undecidable();
      return null;
    }
    deciding.value = true;
    error.value = null;
    try {
      const trimmed = reason.trim();
      // exactOptionalPropertyTypes: an empty reason is omitted, never sent.
      const body: PlanDecision =
        trimmed === ''
          ? { revision: current.revision, decision }
          : { revision: current.revision, decision, reason: trimmed };
      const decided = await tracked(() => client.decidePlan(id, body, newIdempotencyKey()));
      if (runId.value !== id) return null;
      plan.value = decided;
      return decided;
    } catch (cause) {
      if (runId.value !== id) return null;
      error.value = failureOf(cause);
      return null;
    } finally {
      deciding.value = false;
    }
  }

  return {
    client,
    runId,
    plan,
    loading,
    deciding,
    error,
    isAwaitingApproval,
    isApproved,
    blockingFindings,
    warnings,
    canDecide,
    orderedNodes,
    bindRun,
    reset,
    load,
    save,
    decide,
  };
});
