<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type { Approval, ApprovalDecisionChoice, ApprovalState } from '@/api/types';
import { useAuthStore } from '@/stores/auth';
import { useToolsStore } from '@/stores/tools';

const auth = useAuthStore();
const approvals = useToolsStore();

const STATE_LABELS: Record<ApprovalState, string> = {
  pending: 'pending',
  approved: 'approved',
  rejected: 'rejected',
  expired: 'expired',
};

/** confirm holds the decision awaiting confirmation; the server answers last. */
const confirm = ref<{ approval: Approval; decision: ApprovalDecisionChoice } | null>(null);
const confirmReason = ref('');

const canDecide = computed(() => auth.can('approval.decide'));
const confirmVerb = computed<ApprovalDecisionChoice>(() =>
  confirm.value?.decision === 'reject' ? 'reject' : 'approve',
);

onMounted(() => {
  void approvals.load();
});

function isPending(approval: Approval): boolean {
  return approval.state === 'pending';
}

function isBusy(approval: Approval): boolean {
  return approvals.decidingApprovalId === approval.id;
}

function expiryLabel(approval: Approval): string {
  const expiresAt = approval.expires_at;
  if (expiresAt === undefined || expiresAt === null) return 'no expiry';
  return `expires ${new Date(expiresAt).toLocaleString()}`;
}

/** decisionLabel reports what the server recorded; nothing is inferred here. */
function decisionLabel(approval: Approval): string | null {
  if (approval.state === 'pending') return null;
  const decidedBy = approval.decided_by ?? 'system';
  const reason = approval.decision_reason;
  const head = `${STATE_LABELS[approval.state]} by ${decidedBy}`;
  return reason === undefined || reason === '' ? head : `${head} — ${reason}`;
}

function askDecision(approval: Approval, decision: ApprovalDecisionChoice): void {
  confirm.value = { approval, decision };
  confirmReason.value = '';
}

function cancelDecision(): void {
  confirm.value = null;
  confirmReason.value = '';
}

async function submitDecision(): Promise<void> {
  const pending = confirm.value;
  if (pending === null) return;
  try {
    await approvals.decide(pending.approval.id, pending.decision, confirmReason.value);
  } catch {
    // approvals.error and approvals.conflict carry the server explanation; the
    // row keeps whatever state the server recorded.
  } finally {
    // The confirmation belongs to a pending request, so it closes either way.
    cancelDecision();
  }
}

function refresh(): void {
  void approvals.load();
}
</script>

<template>
  <div class="approvals">
    <div class="page-head">
      <div>
        <h2 class="page-title">Approvals</h2>
        <p class="muted page-subtitle">
          High-risk actions wait here for a human decision. {{ approvals.pendingCount }} of
          {{ approvals.items.length }} requests are still pending.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="approvals-refresh"
        :disabled="approvals.loading"
        @click="refresh"
      >
        {{ approvals.loading ? 'Refreshing…' : 'Refresh' }}
      </button>
    </div>

    <p v-if="!canDecide" class="banner banner-info" data-testid="approvals-no-permission">
      <strong>Deciding is restricted.</strong> Only a role that carries the
      <code>approval.decide</code> permission in the active organization can approve or reject a
      request. Every request stays visible below for review, and the API refuses a decision sent
      from an account without that permission — ask an owner or an admin to decide.
    </p>

    <p v-if="approvals.conflict" class="banner banner-conflict" data-testid="approvals-conflict">
      <strong>Decision rejected.</strong> {{ approvals.conflict.message }}
      <button
        class="retry"
        type="button"
        data-testid="approvals-conflict-dismiss"
        @click="approvals.conflict = null"
      >
        Dismiss
      </button>
    </p>

    <p v-if="approvals.error" class="banner banner-error" data-testid="approvals-error">
      <strong>Could not load approvals.</strong> {{ approvals.error }}
      <button class="retry" type="button" data-testid="approvals-retry" @click="refresh">
        Retry
      </button>
    </p>

    <div v-if="approvals.loading" data-testid="approvals-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="approvals.items.length === 0"
      title="No approvals are waiting."
      description="A request appears here as soon as a run needs a human decision on a tool that carries approval."
      test-id="empty-approvals"
    />

    <ul v-else class="approval-list" data-testid="approvals-list">
      <li
        v-for="approval in approvals.items"
        :key="approval.id"
        class="approval-row card"
        :data-testid="`approval-${approval.id}`"
      >
        <div class="approval-main">
          <div class="approval-heading">
            <span class="approval-action">{{ approval.action }}</span>
            <span class="badge" :class="`state-${approval.state}`" data-testid="approval-state">
              {{ STATE_LABELS[approval.state] }}
            </span>
          </div>
          <p class="approval-reason">{{ approval.reason }}</p>
          <p class="approval-meta">
            requested by <code>{{ approval.requested_by }}</code> · created
            {{ new Date(approval.created_at).toLocaleString() }} · {{ expiryLabel(approval) }}
          </p>
          <p
            v-if="decisionLabel(approval)"
            class="approval-decision"
            data-testid="approval-decision"
          >
            {{ decisionLabel(approval) }}
          </p>
        </div>

        <div v-if="canDecide && isPending(approval)" class="approval-actions">
          <button
            class="primary"
            type="button"
            :data-testid="`approve-${approval.id}`"
            :disabled="isBusy(approval)"
            @click="askDecision(approval, 'approve')"
          >
            Approve
          </button>
          <button
            class="danger"
            type="button"
            :data-testid="`reject-${approval.id}`"
            :disabled="isBusy(approval)"
            @click="askDecision(approval, 'reject')"
          >
            Reject
          </button>
        </div>

        <form
          v-if="confirm !== null && confirm.approval.id === approval.id"
          class="decision-form"
          novalidate
          @submit.prevent="submitDecision"
        >
          <p class="decision-label">
            {{ confirmVerb === 'approve' ? 'Approve' : 'Reject' }} {{ approval.action }}?
          </p>
          <FormField
            label="Reason (optional)"
            field-id="decision-reason"
            hint="Your decision and this reason are recorded with the request. The row updates only from the answer the server returns."
          >
            <textarea
              id="decision-reason"
              v-model="confirmReason"
              rows="3"
              maxlength="1000"
              :data-testid="`decision-reason-${approval.id}`"
            />
          </FormField>
          <div class="decision-actions">
            <button
              class="primary"
              type="submit"
              data-testid="decision-confirm"
              :disabled="isBusy(approval)"
            >
              {{ isBusy(approval) ? 'Sending…' : `Confirm ${confirmVerb}` }}
            </button>
            <button
              class="ghost"
              type="button"
              data-testid="decision-cancel"
              :disabled="isBusy(approval)"
              @click="cancelDecision"
            >
              Cancel
            </button>
          </div>
        </form>
      </li>
    </ul>
  </div>
</template>

<style scoped>
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

.approvals {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  padding: 1.25rem;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.banner-error {
  background: var(--color-danger-soft);
  border: 1px solid rgb(180 35 24 / 30%);
  color: var(--color-danger);
}

.banner-conflict {
  background: var(--color-gold-soft);
  border: 1px solid var(--color-gold);
  color: var(--color-navy);
}

.banner-info {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  color: var(--color-text-muted);
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

.approval-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.approval-row {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 0.9rem 1.1rem;
}

.approval-main {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  min-width: 0;
}

.approval-heading {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex-wrap: wrap;
}

.approval-action {
  font-weight: 650;
  color: var(--color-navy);
  font-size: 1rem;
}

.approval-reason {
  margin: 0;
  font-size: 0.88rem;
}

.approval-meta,
.approval-decision {
  margin: 0;
  font-size: 0.76rem;
  color: var(--color-text-muted);
}

.approval-decision {
  font-weight: 600;
  color: var(--color-navy);
}

.approval-actions {
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

.decision-actions {
  display: flex;
  gap: 0.5rem;
}

.state-pending {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 35%);
}

.state-approved {
  background: var(--color-ok-soft);
  color: var(--color-ok);
  border: 1px solid rgb(28 124 70 / 30%);
}

.state-rejected {
  background: var(--color-danger-soft);
  color: var(--color-danger);
  border: 1px solid rgb(180 35 24 / 30%);
}

.state-expired {
  background: #f3f4f6;
  color: var(--color-text-muted);
  border: 1px solid var(--color-gray-line);
}

code {
  background: rgb(10 31 68 / 6%);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
  font-size: 0.85em;
}

.skeleton {
  height: 5rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
