<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type {
  PlanApproval,
  PlanDecisionChoice,
  PlanFinding,
  PlanRisk,
  PlanStatus,
  PlanView,
} from '@/api/types';
import { usePlanStore } from '@/stores/plan';

const route = useRoute();
const store = usePlanStore();

const runId = computed(() => String(route.params.runId ?? ''));

/** pendingChoice is the decision awaiting its reason; the server answers last. */
const pendingChoice = ref<PlanDecisionChoice | null>(null);
const decisionReason = ref('');

watch(
  runId,
  (next) => {
    store.bindRun(next);
    if (next !== '') void store.load(next);
  },
  { immediate: true },
);

const plan = computed<PlanView | null>(() => store.plan);

const STATUS_LABELS: Record<PlanStatus, string> = {
  draft: 'draft',
  awaiting_approval: 'awaiting approval',
  approved: 'approved',
  rejected: 'rejected',
  superseded: 'superseded',
};

const RISK_CLASSES: Record<PlanRisk, string> = {
  high: 'badge-risk-high',
  medium: 'badge-risk-medium',
  low: 'badge-risk-low',
};

/** riskClass keeps the server's risk word and only picks the badge tone. */
function riskClass(risk: string): string {
  if (risk === 'high' || risk === 'medium' || risk === 'low') return RISK_CLASSES[risk];
  return 'badge-risk-unknown';
}

/** shortSha keeps a commit identifier comparable without printing the whole digest. */
function shortSha(value: string): string {
  return value.length <= 12 ? value : `${value.slice(0, 12)}…`;
}

/**
 * The documented plan projection does not repeat the base commit, the
 * out-of-scope list or the assumptions of a revision, so each section says so
 * explicitly instead of quietly rendering nothing.
 */
const baseSha = computed(() => {
  const sha = plan.value?.base_sha;
  return sha === undefined || sha === '' ? 'not returned with this revision' : shortSha(sha);
});

const outOfScope = computed<string[] | null>(() => plan.value?.out_of_scope ?? null);
const assumptions = computed<string[] | null>(() => plan.value?.assumptions ?? null);
const approvals = computed<PlanApproval[]>(() => plan.value?.approvals ?? []);
const findings = computed<PlanFinding[]>(() => plan.value?.findings ?? []);

const errorTitle = computed(() =>
  plan.value === null ? 'Plan unavailable.' : 'Plan request failed.',
);

function loadPlan(): void {
  if (runId.value === '') return;
  void store.load(runId.value);
}

function findingClass(severity: PlanFinding['severity']): string {
  return severity === 'error' ? 'finding-error' : 'finding-warning';
}

function requestDecision(choice: PlanDecisionChoice): void {
  pendingChoice.value = choice;
  decisionReason.value = '';
}

function cancelDecision(): void {
  pendingChoice.value = null;
  decisionReason.value = '';
}

/**
 * submitDecision adopts only what the server returns: the revision, its status
 * and its approvals are replaced by the response, and a refusal keeps the form
 * open with the banner explaining the server's code.
 */
async function submitDecision(): Promise<void> {
  const choice = pendingChoice.value;
  if (choice === null) return;
  const decided = await store.decide(choice, decisionReason.value);
  if (decided !== null) cancelDecision();
}

function createdLabel(value: string): string {
  return new Date(value).toLocaleString();
}
</script>

<template>
  <div class="plan-inspector">
    <div class="page-head">
      <div>
        <h2 class="page-title">Plan inspector</h2>
        <p class="muted page-subtitle">
          The plan revision a run proposed: what it changes, in which order, what proves it, and who
          authorised it.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="plan-reload"
        :disabled="store.loading || store.deciding"
        @click="loadPlan"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <EmptyState
      v-if="runId === ''"
      title="No run selected"
      description="Open a run to inspect the plan revision it proposed and the decision recorded against it."
      test-id="plan-no-run"
    />

    <template v-else>
      <p v-if="store.error" class="banner banner-error" data-testid="plan-error">
        <strong>{{ errorTitle }}</strong>
        <span data-testid="plan-error-code">{{ store.error.code }}</span>
        — {{ store.error.message }}
        <button class="retry" type="button" data-testid="plan-retry" @click="loadPlan">
          Retry
        </button>
      </p>

      <div v-if="store.loading" data-testid="plan-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="plan === null"
        title="No plan for this run yet"
        description="A revision appears here as soon as a run stores one. An empty inspector is the server's answer, never a loading state."
        test-id="plan-empty"
      />

      <template v-else>
        <section class="card panel" data-testid="plan-summary">
          <header class="panel-head">
            <h3 class="card-title">Revision {{ plan.revision }}</h3>
            <span class="badge badge-kind" data-testid="plan-kind">{{ plan.kind }}</span>
            <span class="badge" :class="riskClass(plan.risk)" data-testid="plan-risk">
              {{ plan.risk }} risk
            </span>
            <span class="badge" :class="`status-${plan.status}`" data-testid="plan-status">
              {{ STATUS_LABELS[plan.status] }}
            </span>
          </header>

          <p class="plan-summary-text" data-testid="plan-summary-text">{{ plan.summary }}</p>

          <dl class="facts">
            <div>
              <dt>Revision</dt>
              <dd data-testid="plan-revision">{{ plan.revision }}</dd>
            </div>
            <div>
              <dt>Base commit</dt>
              <dd data-testid="plan-base-sha">{{ baseSha }}</dd>
            </div>
            <div>
              <dt>Run</dt>
              <dd data-testid="plan-run-id">{{ plan.run_id || runId }}</dd>
            </div>
            <div>
              <dt>Recorded</dt>
              <dd data-testid="plan-created-at">{{ createdLabel(plan.created_at) }}</dd>
            </div>
          </dl>
        </section>

        <section class="card panel" data-testid="plan-findings">
          <h3 class="card-title">Findings ({{ findings.length }})</h3>

          <p v-if="findings.length === 0" class="muted" data-testid="plan-findings-none">
            Verification reported no findings against this revision.
          </p>

          <ul v-else class="findings-list">
            <li
              v-for="(finding, index) in findings"
              :key="`${finding.code}-${index}`"
              class="finding"
              :class="findingClass(finding.severity)"
              :data-testid="`finding-${finding.severity}-${finding.code}`"
            >
              <span
                class="badge"
                :class="finding.severity === 'error' ? 'badge-error' : 'badge-warning'"
                data-testid="finding-severity"
              >
                {{ finding.severity }}
              </span>
              <code class="finding-code" data-testid="finding-code">{{ finding.code }}</code>
              <span class="finding-message" data-testid="finding-message">{{
                finding.message
              }}</span>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="plan-nodes">
          <h3 class="card-title">Steps ({{ plan.nodes.length }})</h3>
          <p class="muted panel-note">
            Listed in dependency order: a step appears after every step it names as a prerequisite.
          </p>

          <EmptyState
            v-if="plan.nodes.length === 0"
            title="This revision proposes no steps"
            description="The plan was stored without a single node; the empty list is the server's answer."
            test-id="plan-nodes-empty"
          />

          <ol v-else class="node-list" data-testid="plan-node-list">
            <li
              v-for="(node, index) in store.orderedNodes"
              :key="node.key"
              class="node"
              :data-testid="`node-${node.key}`"
            >
              <div class="node-head">
                <span class="node-index" data-testid="node-index">{{ index + 1 }}</span>
                <span class="node-title" data-testid="node-title">{{ node.title }}</span>
                <span class="badge badge-kind" data-testid="node-kind">{{ node.kind }}</span>
              </div>
              <p class="node-key">
                key <code data-testid="node-key">{{ node.key }}</code>
              </p>
              <p v-if="node.path" class="node-path" data-testid="node-path">
                <code>{{ node.path }}</code>
                <span v-if="node.line_hint !== undefined"> line {{ node.line_hint }}</span>
              </p>
              <p v-else class="muted node-nopath" data-testid="node-nopath">
                No repository path; this step reads or reviews only.
              </p>
              <div v-if="node.evidence && node.evidence.length > 0" data-testid="node-evidence">
                <h4 class="node-subtitle">Evidence</h4>
                <ul class="evidence-list">
                  <li
                    v-for="reference in node.evidence"
                    :key="reference"
                    data-testid="node-evidence-item"
                  >
                    <code>{{ reference }}</code>
                  </li>
                </ul>
              </div>
              <p v-else class="muted node-noevidence" data-testid="node-noevidence">
                No evidence was attached to this step.
              </p>
              <p class="node-prerequisites" data-testid="node-prerequisites">
                <span v-if="node.prerequisites && node.prerequisites.length > 0">
                  after
                  <code
                    v-for="key in node.prerequisites"
                    :key="key"
                    data-testid="node-prerequisite"
                    >{{ key }}</code
                  >
                </span>
                <span v-else class="muted">no prerequisites</span>
              </p>
            </li>
          </ol>
        </section>

        <section class="card panel" data-testid="plan-acceptance">
          <h3 class="card-title">Acceptance criteria ({{ plan.acceptance.length }})</h3>

          <EmptyState
            v-if="plan.acceptance.length === 0"
            title="No acceptance criteria"
            description="This revision records no criterion that proves it works."
            test-id="plan-acceptance-empty"
          />

          <ul v-else class="criterion-list">
            <li
              v-for="criterion in plan.acceptance"
              :key="criterion.key"
              class="criterion"
              :data-testid="`criterion-${criterion.key}`"
            >
              <div class="criterion-head">
                <span class="badge badge-kind" data-testid="criterion-key">{{
                  criterion.key
                }}</span>
                <span class="criterion-description" data-testid="criterion-description">{{
                  criterion.description
                }}</span>
              </div>
              <p class="criterion-verification" data-testid="criterion-verification">
                <template v-if="criterion.verification">
                  verified with <code>{{ criterion.verification }}</code>
                </template>
                <span v-else class="muted">no verification command recorded</span>
              </p>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="plan-out-of-scope">
          <h3 class="card-title">Out of scope</h3>
          <p v-if="outOfScope === null" class="muted" data-testid="plan-out-of-scope-none">
            This revision's projection does not list what it deliberately leaves out.
          </p>
          <p v-else-if="outOfScope.length === 0" class="muted" data-testid="plan-out-of-scope-none">
            The plan leaves nothing out of scope.
          </p>
          <ul v-else class="scope-list">
            <li v-for="entry in outOfScope" :key="entry" data-testid="plan-out-of-scope-item">
              {{ entry }}
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="plan-assumptions">
          <h3 class="card-title">Assumptions</h3>
          <p v-if="assumptions === null" class="muted" data-testid="plan-assumptions-none">
            This revision's projection does not list the assumptions it relies on.
          </p>
          <p v-else-if="assumptions.length === 0" class="muted" data-testid="plan-assumptions-none">
            The plan records no assumptions.
          </p>
          <ul v-else class="scope-list">
            <li v-for="entry in assumptions" :key="entry" data-testid="plan-assumption-item">
              {{ entry }}
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="plan-approvals">
          <h3 class="card-title">Approvals ({{ approvals.length }})</h3>

          <p v-if="approvals.length === 0" class="muted" data-testid="plan-approvals-none">
            No decision has been recorded for this run yet.
          </p>

          <table v-else class="approvals-table">
            <thead>
              <tr>
                <th scope="col">Revision</th>
                <th scope="col">Actor</th>
                <th scope="col">Decision</th>
                <th scope="col">Reason</th>
                <th scope="col">Recorded</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(approval, index) in approvals"
                :key="`${approval.revision}-${index}`"
                :data-testid="`approval-${approval.revision}`"
              >
                <td data-testid="approval-revision">{{ approval.revision }}</td>
                <td data-testid="approval-actor">{{ approval.actor_id }}</td>
                <td data-testid="approval-decision">{{ approval.decision }}</td>
                <td data-testid="approval-reason">{{ approval.reason || '—' }}</td>
                <td data-testid="approval-created-at">{{ createdLabel(approval.created_at) }}</td>
              </tr>
            </tbody>
          </table>
        </section>

        <section class="card panel" data-testid="plan-decision">
          <h3 class="card-title">Decision</h3>

          <p
            v-if="!store.canDecide"
            class="banner banner-info"
            data-testid="plan-decision-restricted"
          >
            <strong>Deciding is restricted.</strong> Only a role that carries the
            <code>approval.decide</code> permission in the active organization can approve or reject
            a plan revision. The revision stays readable above, and the API refuses a decision sent
            from an account without that permission.
          </p>

          <p
            v-else-if="!store.isAwaitingApproval"
            class="muted"
            data-testid="plan-decision-settled"
          >
            Revision {{ plan.revision }} is {{ STATUS_LABELS[plan.status] }}. Only a revision
            awaiting approval can be decided; a new revision supersedes this one and invalidates its
            approvals.
          </p>

          <template v-else>
            <p class="muted panel-note">
              Revision {{ plan.revision }} is waiting for a human decision. Approving or rejecting
              it is recorded with your identity and an optional reason.
            </p>

            <div v-if="pendingChoice === null" class="decision-actions">
              <button
                class="primary"
                type="button"
                data-testid="plan-approve"
                :disabled="store.deciding"
                @click="requestDecision('approve')"
              >
                Approve
              </button>
              <button
                class="danger"
                type="button"
                data-testid="plan-reject"
                :disabled="store.deciding"
                @click="requestDecision('reject')"
              >
                Reject
              </button>
            </div>

            <form
              v-else
              class="decision-form"
              data-testid="plan-decision-form"
              novalidate
              @submit.prevent="submitDecision"
            >
              <p class="decision-label">
                {{ pendingChoice === 'approve' ? 'Approve' : 'Reject' }} revision
                {{ plan.revision }}?
              </p>
              <FormField
                label="Reason (optional)"
                field-id="plan-decision-reason"
                hint="Recorded with the decision. The revision above is replaced only by the answer the server returns."
              >
                <textarea
                  id="plan-decision-reason"
                  v-model="decisionReason"
                  rows="3"
                  maxlength="1000"
                  data-testid="plan-decision-reason"
                />
              </FormField>
              <div class="decision-actions">
                <button
                  class="primary"
                  type="submit"
                  data-testid="plan-decision-confirm"
                  :disabled="store.deciding"
                >
                  {{ store.deciding ? 'Sending…' : `Confirm ${pendingChoice}` }}
                </button>
                <button
                  class="ghost"
                  type="button"
                  data-testid="plan-decision-cancel"
                  :disabled="store.deciding"
                  @click="cancelDecision"
                >
                  Cancel
                </button>
              </div>
            </form>
          </template>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.plan-inspector {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.page-title {
  font-size: 1.5rem;
}

.page-subtitle {
  margin: 0.35rem 0 0;
  font-size: 0.9rem;
  max-width: 76ch;
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  padding: 1.25rem;
}

.panel {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.card-title {
  font-size: 1rem;
}

.panel-note {
  margin: 0;
  font-size: 0.82rem;
  max-width: 90ch;
}

.plan-summary-text {
  margin: 0;
  font-size: 0.92rem;
  line-height: 1.55;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.banner-error {
  background: var(--color-danger-soft);
  border: 1px solid rgb(180 35 24 / 30%);
  color: var(--color-danger);
}

.banner-info {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
  max-width: 90ch;
}

.retry {
  border: 1px solid currentcolor;
  background: transparent;
  color: inherit;
  padding: 0.25rem 0.7rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.ghost {
  border: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  color: var(--color-navy);
  padding: 0.35rem 0.8rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.ghost:hover:not(:disabled) {
  border-color: var(--color-navy);
}

.primary {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.5rem 1rem;
  border-radius: var(--radius-sm);
  font-weight: 600;
}

.primary:hover:not(:disabled) {
  background: var(--color-navy-soft);
}

.danger {
  border: 1px solid var(--color-danger);
  background: var(--color-surface);
  color: var(--color-danger);
  padding: 0.5rem 1rem;
  border-radius: var(--radius-sm);
  font-weight: 600;
}

.danger:hover:not(:disabled) {
  background: var(--color-danger-soft);
}

.facts {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: 0.6rem 1.25rem;
}

.facts dt {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
}

.facts dd {
  margin: 0.1rem 0 0;
  font-size: 0.85rem;
  word-break: break-all;
}

.badge {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.15rem 0.55rem;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: lowercase;
}

.badge-kind {
  background: var(--color-navy);
  color: #fff;
  border: 1px solid var(--color-navy);
}

.badge-risk-high {
  background: var(--color-danger-soft);
  color: var(--color-danger);
  border: 1px solid rgb(180 35 24 / 30%);
}

.badge-risk-medium {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 45%);
}

.badge-risk-low {
  background: #f3f4f6;
  color: var(--color-text-muted);
  border: 1px solid var(--color-gray-line);
}

.badge-risk-unknown {
  background: #f3f4f6;
  color: var(--color-text-muted);
  border: 1px solid var(--color-gray-line);
}

.badge-error {
  background: var(--color-danger-soft);
  color: var(--color-danger);
  border: 1px solid rgb(180 35 24 / 30%);
}

.badge-warning {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 45%);
}

.status-draft,
.status-superseded {
  background: #f3f4f6;
  color: var(--color-text-muted);
  border: 1px solid var(--color-gray-line);
}

.status-awaiting_approval {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 45%);
}

.status-approved {
  background: var(--color-ok-soft);
  color: var(--color-ok);
  border: 1px solid rgb(28 124 70 / 30%);
}

.status-rejected {
  background: var(--color-danger-soft);
  color: var(--color-danger);
  border: 1px solid rgb(180 35 24 / 30%);
}

.findings-list,
.node-list,
.criterion-list,
.scope-list,
.evidence-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}

.finding {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  flex-wrap: wrap;
  padding: 0.6rem 0.75rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-gray-line);
  font-size: 0.85rem;
}

.finding-error {
  background: var(--color-danger-soft);
  border-color: rgb(180 35 24 / 30%);
}

.finding-warning {
  background: var(--color-gold-soft);
  border-color: rgb(201 162 39 / 45%);
}

.finding-code {
  font-weight: 650;
}

.node {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  padding: 0.75rem 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.node-head {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex-wrap: wrap;
}

.node-index {
  display: grid;
  place-items: center;
  width: 1.5rem;
  height: 1.5rem;
  border-radius: 999px;
  background: var(--color-canvas);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 700;
}

.node-title {
  font-weight: 650;
  color: var(--color-navy);
}

.node-key,
.node-path,
.node-prerequisites {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
  display: flex;
  gap: 0.35rem;
  flex-wrap: wrap;
  align-items: baseline;
}

.node-path {
  color: var(--color-text);
}

.node-subtitle {
  margin: 0 0 0.3rem;
  font-size: 0.74rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
}

.evidence-list,
.scope-list {
  font-size: 0.8rem;
  padding-left: 0.2rem;
}

.scope-list li {
  padding-left: 1rem;
  position: relative;
}

.scope-list li::before {
  content: '•';
  position: absolute;
  left: 0;
  color: var(--color-gold);
}

.criterion {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  padding: 0.65rem 0.8rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.criterion-head {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  flex-wrap: wrap;
}

.criterion-description {
  font-size: 0.88rem;
}

.criterion-verification {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.approvals-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.82rem;
}

.approvals-table th {
  text-align: left;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-muted);
  border-bottom: 1px solid var(--color-gray-line);
  padding: 0.4rem 0.6rem 0.4rem 0;
}

.approvals-table td {
  padding: 0.45rem 0.6rem 0.45rem 0;
  border-bottom: 1px solid var(--color-gray-line);
  vertical-align: top;
  word-break: break-all;
}

.decision-actions {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.decision-form {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  max-width: 34rem;
  border-top: 1px dashed var(--color-gray-line);
  padding-top: 0.75rem;
}

.decision-label {
  margin: 0;
  font-size: 0.85rem;
  font-weight: 650;
  color: var(--color-navy);
}

textarea {
  font: inherit;
  width: 100%;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
  resize: vertical;
}

textarea:focus {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.skeleton {
  height: 6rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
