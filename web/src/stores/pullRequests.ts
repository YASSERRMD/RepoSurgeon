import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type {
  FailureClass,
  MergePullRequestBody,
  MergeResult,
  OpenPullRequestBody,
  PullRequestView,
} from '@/api/types';
import { useAuthStore } from './auth';
import { failureOf } from './context';

/**
 * PullRequestFailure is the structured error envelope the pull-request view
 * renders. The server's own status and code travel with it: 400
 * `pr_push_refused` (a local check did not run green, the secret scan found
 * something, a path is not allowlisted or the base moved), 409
 * `pr_merge_refused` (a required GitHub check failed, an approval is missing or
 * no human authorized the merge).
 */
export type PullRequestFailure = ReturnType<typeof failureOf>;

/** MERGE_STATES are the tracked pull-request states. */
export const PULL_REQUEST_STATES = ['open', 'merged', 'closed', 'blocked', 'abandoned'] as const;

/** mergeMethodLabels renders the GitHub merge methods the API accepts. */
export const MERGE_METHODS = ['merge', 'squash', 'rebase'] as const;

function newEffectId(): string {
  const random = Math.random().toString(36).slice(2, 10);
  return `effect_${Date.now().toString(36)}_${random}`;
}

/**
 * The pull-request store owns the issue-to-PR lifecycle: the tracked pull
 * request with its real GitHub checks and reviews, the gate refusals, and the
 * merge result. Nothing is derived locally — a check that GitHub has not
 * reported is shown as pending, never as a pass.
 */
export const usePullRequestStore = defineStore('pullRequests', () => {
  const client: HealthClient = createHealthClient();
  const runId = ref<string | null>(null);
  const pullRequest = ref<PullRequestView | null>(null);
  const mergeResult = ref<MergeResult | null>(null);
  const opening = ref(false);
  const merging = ref(false);
  const abandoning = ref(false);
  const error = ref<PullRequestFailure | null>(null);
  const gateReasons = ref<string[]>([]);
  const inFlight = ref(0);

  /** loading follows the requests still outstanding for this view. */
  const loading = computed<boolean>(() => inFlight.value > 0);

  const checks = computed(() => pullRequest.value?.checks ?? []);
  const reviews = computed(() => pullRequest.value?.reviews ?? []);
  const commits = computed(() => pullRequest.value?.commits ?? []);
  const state = computed<string>(() => pullRequest.value?.state ?? '');
  const isMerged = computed<boolean>(() => state.value === 'merged');
  const isBlocked = computed<boolean>(() => state.value === 'blocked');
  const isAbandoned = computed<boolean>(() => state.value === 'abandoned');
  const approvalCount = computed<number>(() => pullRequest.value?.approval_count ?? 0);
  const branchDeleted = computed<boolean>(() => pullRequest.value?.branch_deleted ?? false);

  /** canAct is the permission gate only; the API answers authoritatively. */
  const canOpen = computed<boolean>(() => useAuthStore().can('run.execute'));
  const canMerge = computed<boolean>(() => useAuthStore().can('merge.execute'));

  /** isTerminal stops both actions once the change is merged or abandoned. */
  const isTerminal = computed<boolean>(() => isMerged.value || isAbandoned.value);

  /** failingChecks are the real GitHub checks that concluded unsuccessfully. */
  const failingChecks = computed(() =>
    checks.value.filter((check) => check.status === 'completed' && check.conclusion === 'failure'),
  );

  /** pendingChecks are the checks GitHub has not finished reporting. */
  const pendingChecks = computed(() =>
    checks.value.filter((check) => check.status !== 'completed'),
  );

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
    pullRequest.value = null;
    mergeResult.value = null;
    error.value = null;
    gateReasons.value = [];
  }

  async function load(next: string): Promise<void> {
    if (next === '') {
      bindRun(null);
      return;
    }
    bindRun(next);
    try {
      const loaded = await tracked(() => client.getPullRequest(next));
      if (runId.value !== next) return;
      pullRequest.value = loaded;
      error.value = null;
      gateReasons.value = [];
    } catch (cause) {
      if (runId.value !== next) return;
      pullRequest.value = null;
      error.value = failureOf(cause);
      gateReasons.value = failureOf(cause).retryable ? [] : [];
    }
  }

  /**
   * open composes the branch and opens the pull request. The effect id is
   * minted here and recorded by the server, so a retry of the same action
   * answers with the same pull request instead of a second one.
   */
  async function open(request: {
    summary: string;
    title: string;
    issueNumber?: number;
    files: string[];
    risks: string[];
    checks: OpenPullRequestBody['checks'];
    commits: OpenPullRequestBody['commits'];
    workspaceId: string;
    head: string;
    base?: string;
    repoOwner: string;
    repoName: string;
    secretScanClean: boolean;
    pinnedSha: string;
    baseSha: string;
    failureClass?: FailureClass;
  }): Promise<PullRequestView | null> {
    const current = runId.value;
    if (current === null) return null;
    opening.value = true;
    try {
      const body: OpenPullRequestBody = {
        workspace_id: request.workspaceId,
        effect_id: newEffectId(),
        head: request.head,
        title: request.title,
        repo_owner: request.repoOwner,
        repo_name: request.repoName,
        summary: request.summary,
        risks: request.risks,
        secret_scan_clean: request.secretScanClean,
        pinned_sha: request.pinnedSha,
        base_sha: request.baseSha,
        files: request.files,
        checks: request.checks,
        commits: request.commits,
        ...(request.issueNumber === undefined ? {} : { issue_number: request.issueNumber }),
        ...(request.base === undefined ? {} : { base: request.base }),
      };
      const opened = await tracked(() =>
        client.openPullRequest(current, body, newIdempotencyKey()),
      );
      if (runId.value !== current) return null;
      pullRequest.value = opened;
      error.value = null;
      gateReasons.value = [];
      return opened;
    } catch (cause) {
      const failure = failureOf(cause);
      if (runId.value !== current) return null;
      error.value = failure;
      gateReasons.value = readReasons(cause);
      return null;
    } finally {
      opening.value = false;
    }
  }

  /**
   * merge requests the merge under an explicit human authorization. The server
   * refuses when a required check is not green, an approval is missing, or the
   * authorizer is empty, a bot or a model.
   */
  async function merge(request: {
    authorizedBy: string;
    method?: string;
  }): Promise<MergeResult | null> {
    const current = runId.value;
    if (current === null) return null;
    if (request.authorizedBy.trim() === '') return null;
    merging.value = true;
    try {
      const body: MergePullRequestBody = {
        authorized_by: request.authorizedBy,
        method: request.method ?? 'merge',
      };
      const result = await tracked(() =>
        client.mergePullRequest(current, body, newIdempotencyKey()),
      );
      if (runId.value !== current) return null;
      mergeResult.value = result;
      if (result.pull_request !== undefined) {
        pullRequest.value = result.pull_request;
      }
      error.value = null;
      gateReasons.value = readReasons(result.decision);
      return result;
    } catch (cause) {
      if (runId.value !== current) return null;
      error.value = failureOf(cause);
      gateReasons.value = readReasons(cause);
      return null;
    } finally {
      merging.value = false;
    }
  }

  /** abandon closes the pull request without merging and keeps the branch. */
  async function abandon(reason: string): Promise<PullRequestView | null> {
    const current = runId.value;
    if (current === null || reason.trim() === '') return null;
    abandoning.value = true;
    try {
      const abandoned = await tracked(() =>
        client.abandonPullRequest(current, { reason }, newIdempotencyKey()),
      );
      if (runId.value !== current) return null;
      pullRequest.value = abandoned;
      error.value = null;
      return abandoned;
    } catch (cause) {
      if (runId.value !== current) return null;
      error.value = failureOf(cause);
      return null;
    } finally {
      abandoning.value = false;
    }
  }

  return {
    runId,
    pullRequest,
    mergeResult,
    error,
    gateReasons,
    loading,
    opening,
    merging,
    abandoning,
    checks,
    reviews,
    commits,
    state,
    isMerged,
    isBlocked,
    isAbandoned,
    isTerminal,
    approvalCount,
    branchDeleted,
    canOpen,
    canMerge,
    failingChecks,
    pendingChecks,
    bindRun,
    load,
    open,
    merge,
    abandon,
  };
});

/** readReasons extracts the gate's recorded reasons from an error or decision. */
function readReasons(source: unknown): string[] {
  if (source === null || source === undefined) return [];
  const record = source as { details?: unknown; reasons?: unknown };
  if (Array.isArray(record.reasons)) {
    return record.reasons.filter((value): value is string => typeof value === 'string');
  }
  const details = record.details as { reasons?: unknown } | undefined;
  if (details !== undefined && Array.isArray(details.reasons)) {
    return details.reasons.filter((value): value is string => typeof value === 'string');
  }
  return [];
}
