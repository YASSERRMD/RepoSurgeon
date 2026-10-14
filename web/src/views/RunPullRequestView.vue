<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type { RemoteCheck, RemoteReview } from '@/api/types';
import { MERGE_METHODS, usePullRequestStore } from '@/stores/pullRequests';

const route = useRoute();
const store = usePullRequestStore();

const runId = computed(() => String(route.params.runId ?? ''));

watch(
  runId,
  (next) => {
    store.bindRun(next);
    if (next !== '') void store.load(next);
  },
  { immediate: true },
);

const mergeAuthorizer = ref('');
const mergeMethod = ref<string>(MERGE_METHODS[0]);
const authorizerError = ref('');
const abandonReason = ref('');
const abandonError = ref('');

const pr = computed(() => store.pullRequest);

/** checkLabel renders a GitHub check without changing what GitHub said. */
function checkLabel(status: string | undefined): string {
  switch (status) {
    case 'success':
      return 'passed';
    case 'failure':
      return 'failed';
    case 'cancelled':
      return 'canceled';
    case 'skipped':
      return 'skipped';
    case 'neutral':
      return 'neutral';
    case 'in_progress':
    case 'queued':
      return 'pending';
    default:
      return 'not reported';
  }
}

function checkTone(check: RemoteCheck): string {
  if (check.status !== 'completed') return 'badge-pending';
  return check.conclusion === 'success' ? 'badge-ok' : 'badge-error';
}

function reviewLabel(review: RemoteReview): string {
  switch (review.state) {
    case 'approved':
      return 'approved';
    case 'changes_requested':
      return 'changes requested';
    case 'commented':
      return 'commented';
    default:
      return review.state ?? 'unknown';
  }
}

function shortSha(value: string | undefined): string {
  if (value === undefined || value === '') return 'not recorded';
  return value.length <= 12 ? value : `${value.slice(0, 12)}…`;
}

const canSubmitMerge = computed<boolean>(() => {
  if (!store.canMerge || store.isTerminal || store.merging) return false;
  if (store.pullRequest === null) return false;
  if (store.failingChecks.length > 0 || store.pendingChecks.length > 0) return false;
  return store.approvalCount > 0;
});

async function submitMerge(): Promise<void> {
  if (mergeAuthorizer.value.trim() === '') {
    authorizerError.value = 'a permitted human must be named; a model or bot identity is refused';
    return;
  }
  authorizerError.value = '';
  await store.merge({ authorizedBy: mergeAuthorizer.value, method: mergeMethod.value });
}

async function submitAbandon(): Promise<void> {
  if (abandonReason.value.trim() === '') {
    abandonError.value = 'a reason is required so the abandoned change stays explainable';
    return;
  }
  abandonError.value = '';
  await store.abandon(abandonReason.value);
}
</script>

<template>
  <div class="pull-request-inspector">
    <div class="page-head">
      <div>
        <h2 class="page-title">Pull request</h2>
        <p class="muted page-subtitle">
          The change this run produced: the atomic commits behind it, the real GitHub checks and
          reviews, and who authorized the merge.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="pr-reload"
        :disabled="store.loading || store.opening || store.merging || store.abandoning"
        @click="store.load(runId)"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <EmptyState
      v-if="runId === ''"
      title="No run selected"
      description="Open a run to inspect the pull request it produced and the merge decision recorded against it."
      test-id="pr-no-run"
    />

    <template v-else>
      <p v-if="store.error" class="banner banner-error" data-testid="pr-error">
        <strong>Pull request request failed.</strong>
        <span data-testid="pr-error-code">{{ store.error.code }}</span>
        — {{ store.error.message }}
        <button class="retry" type="button" data-testid="pr-retry" @click="store.load(runId)">
          Retry
        </button>
      </p>

      <ul
        v-if="store.gateReasons.length > 0"
        class="banner banner-error"
        data-testid="pr-gate-reasons"
      >
        <li v-for="reason in store.gateReasons" :key="reason" data-testid="pr-gate-reason">
          {{ reason }}
        </li>
      </ul>

      <div v-if="store.loading && pr === null" data-testid="pr-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="pr === null"
        title="No pull request for this run yet"
        description="One appears here as soon as a run composes its branch and opens the pull request. An empty inspector is the server's answer, never a loading state."
        test-id="pr-empty"
      />

      <template v-else>
        <section class="card panel" data-testid="pr-summary">
          <header class="panel-head">
            <h3 class="card-title">{{ pr.title }}</h3>
            <a
              v-if="pr.url !== undefined && pr.url !== ''"
              class="pr-link"
              data-testid="pr-link"
              :href="pr.url"
              target="_blank"
              rel="noreferrer"
            >
              {{ pr.repo_owner ?? '' }}/{{ pr.repo_name ?? '' }}#{{ pr.number }}
            </a>
            <span class="badge" :class="`status-${pr.state ?? 'unknown'}`" data-testid="pr-state">
              {{ pr.state ?? 'unknown' }}
            </span>
          </header>

          <dl class="facts">
            <div>
              <dt>Branch</dt>
              <dd data-testid="pr-branch">
                {{ pr.head_branch ?? '' }} → {{ pr.base_branch ?? '' }}
              </dd>
            </div>
            <div>
              <dt>Issue</dt>
              <dd data-testid="pr-issue">{{ pr.issue_number ?? 'none' }}</dd>
            </div>
            <div>
              <dt>Approvals</dt>
              <dd data-testid="pr-approvals">{{ pr.approval_count ?? 0 }}</dd>
            </div>
            <div>
              <dt>Merge state</dt>
              <dd data-testid="pr-merge-state">{{ pr.merge_state || 'not reported' }}</dd>
            </div>
            <div>
              <dt>Merge SHA</dt>
              <dd data-testid="pr-merge-sha">{{ pr.merge_sha || 'not merged' }}</dd>
            </div>
            <div>
              <dt>Branch deleted</dt>
              <dd data-testid="pr-branch-deleted">{{ pr.branch_deleted ? 'yes' : 'no' }}</dd>
            </div>
          </dl>

          <p v-if="pr.blocked_reason" class="pr-blocked" data-testid="pr-blocked-reason">
            {{ pr.blocked_reason }}
          </p>
        </section>

        <section class="card panel" data-testid="pr-checks">
          <h3 class="card-title">Checks ({{ store.checks.length }})</h3>

          <p v-if="store.checks.length === 0" class="muted" data-testid="pr-checks-none">
            GitHub has not reported any check for this branch yet.
          </p>

          <ul v-else class="check-list">
            <li
              v-for="check in store.checks"
              :key="check.name"
              class="check"
              :data-testid="`check-${check.name}`"
            >
              <span class="badge" :class="checkTone(check)" data-testid="check-status">
                {{ checkLabel(check.conclusion ?? check.status) }}
              </span>
              <code data-testid="check-name">{{ check.name }}</code>
              <a
                v-if="check.details_url"
                class="pr-link"
                :href="check.details_url"
                target="_blank"
                rel="noreferrer"
                data-testid="check-details"
              >
                details
              </a>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="pr-reviews">
          <h3 class="card-title">Reviews ({{ store.reviews.length }})</h3>

          <p v-if="store.reviews.length === 0" class="muted" data-testid="pr-reviews-none">
            No review has been recorded for this branch yet.
          </p>

          <ul v-else class="review-list">
            <li
              v-for="review in store.reviews"
              :key="`${review.author}-${review.state}`"
              class="review"
              :data-testid="`review-${review.state}`"
            >
              <span
                class="badge"
                :class="review.state === 'approved' ? 'badge-ok' : 'badge-warning'"
                data-testid="review-state"
              >
                {{ reviewLabel(review) }}
              </span>
              <code data-testid="review-author">{{ review.author }}</code>
              <span class="muted" data-testid="review-commit">{{
                shortSha(review.commit_id)
              }}</span>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="pr-commits">
          <h3 class="card-title">Commits ({{ store.commits.length }})</h3>

          <p v-if="store.commits.length === 0" class="muted" data-testid="pr-commits-none">
            No composed commit was recorded for this pull request yet.
          </p>

          <ol v-else class="commit-list">
            <li
              v-for="commit in store.commits"
              :key="commit.sha ?? commit.message ?? ''"
              class="commit"
              :data-testid="`commit-${shortSha(commit.sha)}`"
            >
              <code data-testid="commit-sha">{{ shortSha(commit.sha) }}</code>
              <span data-testid="commit-message">{{ commit.message }}</span>
              <span class="muted" data-testid="commit-author">
                {{ commit.author?.name ?? 'unknown' }} &lt;{{
                  commit.author?.email ?? 'unknown'
                }}&gt;
              </span>
            </li>
          </ol>
        </section>

        <section class="card panel" data-testid="pr-actions">
          <h3 class="card-title">Merge</h3>

          <p
            v-if="store.mergeResult !== null"
            class="banner banner-ok"
            data-testid="pr-merge-result"
          >
            <strong
              >Merge {{ store.mergeResult.already_merged ? 'already recorded' : 'applied' }}</strong
            >
            <span data-testid="pr-merge-result-sha">{{ store.mergeResult.merge_sha || '' }}</span>
            <span data-testid="pr-merge-branch-deleted">
              {{ store.pullRequest?.branch_deleted ? 'branch deleted' : 'branch kept' }}
            </span>
          </p>

          <form class="action-form" @submit.prevent="submitMerge">
            <FormField
              label="Authorized by (required)"
              field-id="pr-merge-authorizer"
              hint="A permitted human. A model, bot or empty identity is refused by the server."
              :error="authorizerError"
            >
              <input
                id="pr-merge-authorizer"
                v-model="mergeAuthorizer"
                type="text"
                maxlength="254"
                data-testid="pr-merge-authorizer-input"
              />
            </FormField>

            <FormField
              label="Merge method"
              field-id="pr-merge-method"
              hint="The GitHub merge method recorded with the merge action."
            >
              <select
                id="pr-merge-method"
                v-model="mergeMethod"
                data-testid="pr-merge-method-select"
              >
                <option v-for="method in MERGE_METHODS" :key="method" :value="method">
                  {{ method }}
                </option>
              </select>
            </FormField>

            <div class="action-row">
              <button
                class="primary"
                type="submit"
                data-testid="pr-merge-submit"
                :disabled="!canSubmitMerge || store.loading"
              >
                {{ store.merging ? 'Merging…' : 'Merge' }}
              </button>
              <span v-if="!store.canMerge" class="muted" data-testid="pr-merge-no-permission">
                Merging requires the merge.execute permission.
              </span>
              <span v-else-if="store.isTerminal" class="muted" data-testid="pr-merge-terminal">
                The pull request is already {{ store.state }}.
              </span>
            </div>
          </form>

          <form class="action-form" @submit.prevent="submitAbandon">
            <FormField
              label="Abandon reason (required)"
              field-id="pr-abandon-reason"
              hint="Recorded with the abandoned change. The branch and its evidence are kept."
              :error="abandonError"
            >
              <textarea
                id="pr-abandon-reason"
                v-model="abandonReason"
                rows="2"
                maxlength="1000"
                data-testid="pr-abandon-reason-input"
              />
            </FormField>

            <div class="action-row">
              <button
                class="primary"
                type="submit"
                data-testid="pr-abandon-submit"
                :disabled="!store.canOpen || store.isTerminal || store.abandoning || store.loading"
              >
                {{ store.abandoning ? 'Abandoning…' : 'Abandon without merging' }}
              </button>
            </div>
          </form>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.pr-link {
  color: var(--color-navy);
  font-weight: 600;
  text-decoration: underline;
}

.pr-blocked {
  margin: 0;
  padding: 0.5rem 0.65rem;
  border-left: 3px solid var(--color-gold);
  color: var(--color-navy);
  font-size: 0.85rem;
}

.check-list,
.review-list,
.commit-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.check,
.review,
.commit {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 0.65rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-surface);
}

.commit {
  flex-direction: column;
  align-items: flex-start;
}

.action-form {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding-bottom: 0.75rem;
  border-bottom: 1px solid var(--color-border);
}

.action-form:last-of-type {
  border-bottom: none;
  padding-bottom: 0;
}

.action-row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}
</style>
