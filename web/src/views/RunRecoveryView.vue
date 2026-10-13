<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import type { FailureClass } from '@/api/types';
import { FAILURE_CLASSES, useRecoveryStore } from '@/stores/recovery';

const route = useRoute();
const store = useRecoveryStore();

const runId = computed(() => String(route.params.runId ?? ''));

watch(
  runId,
  (next) => {
    store.bindRun(next);
    if (next !== '') void store.load(next);
  },
  { immediate: true },
);

const KIND_LABELS: Record<string, string> = {
  retry_scheduled: 'retry scheduled',
  retry_exhausted: 'retry budget exhausted',
  loop_detected: 'loop detected',
  blocked: 'blocked',
  recovered: 'recovered',
  rollback: 'rolled back',
  lease_recovered: 'lease recovered',
  receipt_replayed: 'effect replayed',
  credential_refreshed: 'credential refreshed',
};

/** kindLabel keeps the server's word and only renders a readable form. */
function kindLabel(kind: string | undefined): string {
  if (kind === undefined) return 'unknown entry';
  return KIND_LABELS[kind] ?? kind;
}

const recoverReason = ref('');
const recoverClass = ref<FailureClass>('test_failure');
const recoverReasonError = ref('');
const rollbackReason = ref('');
const rollbackEffectError = ref('');
const selectedCheckpoint = ref('');

/** createdLabel renders the server timestamp, and says so when it is absent. */
function createdLabel(value: string | undefined): string {
  return value === undefined || value === '' ? 'not recorded' : new Date(value).toLocaleString();
}

/**
 * submitRecover sends the operator's reason. The server decides whether the
 * attempt may run, so the button stays disabled while the answer is in flight
 * and the response — including a refusal — is reported verbatim.
 */
async function submitRecover(): Promise<void> {
  if (recoverReason.value.trim() === '') {
    recoverReasonError.value = 'a reason is required so the retry is auditable';
    return;
  }
  recoverReasonError.value = '';
  await store.recover({ class: recoverClass.value, reason: recoverReason.value });
}

/**
 * submitRollback restores the checkpoint the operator picked. The effect id is
 * minted per click, so repeating the action is a new restore and a replay of
 * the same request is reported as a duplicate.
 */
async function submitRollback(): Promise<void> {
  if (rollbackReason.value.trim() === '') {
    rollbackEffectError.value = 'a reason is required so the rollback is auditable';
    return;
  }
  rollbackEffectError.value = '';
  await store.rollback({
    reason: rollbackReason.value,
    ...(selectedCheckpoint.value === '' ? {} : { checkpointId: selectedCheckpoint.value }),
  });
}

function pickCheckpoint(checkpointId: string | undefined): void {
  if (checkpointId === undefined) return;
  selectedCheckpoint.value = checkpointId;
}

const errorTitle = computed(() =>
  store.runState === '' ? 'Recovery unavailable.' : 'Recovery request failed.',
);
</script>

<template>
  <div class="recovery-inspector">
    <div class="page-head">
      <div>
        <h2 class="page-title">Recovery inspector</h2>
        <p class="muted page-subtitle">
          What the repair loop recorded: the journalled timeline, the bounded attempt budget of each
          error class, and the checkpoints a run can be restored to.
        </p>
      </div>
      <button
        class="ghost"
        type="button"
        data-testid="recovery-reload"
        :disabled="store.loading || store.recovering || store.rollingBack"
        @click="store.load(runId)"
      >
        {{ store.loading ? 'Reloading…' : 'Reload' }}
      </button>
    </div>

    <EmptyState
      v-if="runId === ''"
      title="No run selected"
      description="Open a run to inspect its recovery timeline, attempt budgets and checkpoints."
      test-id="recovery-no-run"
    />

    <template v-else>
      <p v-if="store.error" class="banner banner-error" data-testid="recovery-error">
        <strong>{{ errorTitle }}</strong>
        <span data-testid="recovery-error-code">{{ store.error.code }}</span>
        — {{ store.error.message }}
        <button class="retry" type="button" data-testid="recovery-retry" @click="store.load(runId)">
          Retry
        </button>
      </p>

      <p
        v-if="store.lastRollback !== null"
        class="banner"
        :class="store.lastRollback.duplicate ? 'banner-warning' : 'banner-ok'"
        data-testid="recovery-rollback-result"
      >
        <strong>Rollback {{ store.lastRollback.duplicate ? 'replayed' : 'applied' }}</strong>
        <span data-testid="recovery-rollback-checkpoint">{{
          store.lastRollback.checkpoint_id
        }}</span>
        — {{ store.lastRollback.restored_files?.length ?? 0 }} file(s) restored,
        {{ store.lastRollback.drift_after ?? 0 }} still differing.
        <span v-if="store.lastRollback.duplicate" data-testid="recovery-rollback-duplicate">
          This effect was already applied; the recorded outcome is replayed instead of writing the
          workspace a second time.
        </span>
      </p>

      <div v-if="store.loading && store.view === null" data-testid="recovery-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="store.view === null"
        title="No recovery state for this run yet"
        description="A timeline appears here as soon as the run records its first attempt. An empty inspector is the server's answer, never a loading state."
        test-id="recovery-empty"
      />

      <template v-else>
        <section class="card panel" data-testid="recovery-summary">
          <header class="panel-head">
            <h3 class="card-title">Run {{ store.view.run_id }}</h3>
            <span
              class="badge"
              :class="`status-${store.view.run_state}`"
              data-testid="recovery-run-state"
            >
              {{ store.view.run_state }}
            </span>
            <span class="badge badge-kind" data-testid="recovery-next-attempt">
              next attempt {{ store.view.next_attempt }}
            </span>
          </header>

          <dl class="facts">
            <div>
              <dt>Attempts recorded</dt>
              <dd data-testid="recovery-attempt-count">{{ store.view.attempts.length }}</dd>
            </div>
            <div>
              <dt>Timeline entries</dt>
              <dd data-testid="recovery-timeline-count">{{ store.view.timeline.length }}</dd>
            </div>
            <div>
              <dt>Checkpoints</dt>
              <dd data-testid="recovery-checkpoint-count">{{ store.view.checkpoints.length }}</dd>
            </div>
          </dl>
        </section>

        <section
          v-if="store.view.run_state === 'blocked' || store.blockedEvent !== null"
          class="card panel panel-blocked"
          data-testid="recovery-blocked"
        >
          <h3 class="card-title">Blocked</h3>
          <p class="blocked-reason" data-testid="recovery-blocked-reason">
            {{
              store.blockedEvent?.reason ??
              'The server parked this run as blocked; the journal below is the authoritative record.'
            }}
          </p>
          <dl v-if="store.blockedEvent !== null" class="facts">
            <div>
              <dt>Journal kind</dt>
              <dd data-testid="recovery-blocked-kind">{{ store.blockedEvent.kind }}</dd>
            </div>
            <div>
              <dt>Attempt</dt>
              <dd data-testid="recovery-blocked-attempt">{{ store.blockedEvent.attempt }}</dd>
            </div>
            <div>
              <dt>Action</dt>
              <dd data-testid="recovery-blocked-action">
                {{ store.isTerminal ? 'terminal' : 'roll back to a checkpoint' }}
              </dd>
            </div>
          </dl>
        </section>

        <section class="card panel" data-testid="recovery-budgets">
          <h3 class="card-title">Attempt budgets ({{ store.view.budgets.length }})</h3>

          <p
            v-if="store.view.budgets.length === 0"
            class="muted"
            data-testid="recovery-budgets-none"
          >
            No attempt budget has been consumed yet, so every class still has its full allowance.
          </p>

          <ul v-else class="budget-list">
            <li
              v-for="budget in store.view.budgets"
              :key="budget.class"
              class="budget"
              :class="budget.exhausted ? 'budget-exhausted' : 'budget-available'"
              :data-testid="`budget-${budget.class}`"
            >
              <code class="budget-class" data-testid="budget-class">{{ budget.class }}</code>
              <span class="budget-use" data-testid="budget-use">
                {{ budget.used }} of {{ budget.allowed }} used
              </span>
              <span class="badge" :class="budget.exhausted ? 'badge-error' : 'badge-ok'">
                {{ budget.exhausted ? 'exhausted' : `${budget.remaining} left` }}
              </span>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="recovery-attempts">
          <h3 class="card-title">Attempts ({{ store.view.attempts.length }})</h3>

          <p
            v-if="store.view.attempts.length === 0"
            class="muted"
            data-testid="recovery-attempts-none"
          >
            The run has not recorded a repair attempt yet.
          </p>

          <table v-else class="grid">
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">Kind</th>
                <th scope="col">Verdict</th>
                <th scope="col">Failing</th>
                <th scope="col">Diff</th>
                <th scope="col">Note</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="attempt in store.view.attempts"
                :key="attempt.number"
                :data-testid="`attempt-${attempt.number}`"
              >
                <td data-testid="attempt-number">{{ attempt.number }}</td>
                <td data-testid="attempt-kind">{{ attempt.kind }}</td>
                <td data-testid="attempt-verdict">{{ attempt.verdict ?? 'unknown' }}</td>
                <td data-testid="attempt-failing">{{ attempt.failing_count ?? 0 }}</td>
                <td data-testid="attempt-patch">{{ attempt.patch_fingerprint ?? '—' }}</td>
                <td data-testid="attempt-note">{{ attempt.note ?? '' }}</td>
              </tr>
            </tbody>
          </table>
        </section>

        <section class="card panel" data-testid="recovery-timeline">
          <h3 class="card-title">Timeline ({{ store.view.timeline.length }})</h3>

          <p
            v-if="store.view.timeline.length === 0"
            class="muted"
            data-testid="recovery-timeline-none"
          >
            The state recovery journal is empty for this run.
          </p>

          <ol v-else class="timeline">
            <li
              v-for="event in store.view.timeline"
              :key="event.id"
              class="timeline-entry"
              :data-testid="`event-${event.kind}`"
            >
              <span class="badge badge-kind" data-testid="event-kind">{{
                kindLabel(event.kind)
              }}</span>
              <span class="timeline-reason" data-testid="event-reason">
                {{ event.reason ?? '' }}
              </span>
              <span class="muted timeline-meta" data-testid="event-meta">
                attempt {{ event.attempt }} · {{ createdLabel(event.created_at) }}
              </span>
            </li>
          </ol>
        </section>

        <section class="card panel" data-testid="recovery-receipts">
          <h3 class="card-title">Effect receipts ({{ store.view.receipts.length }})</h3>

          <p
            v-if="store.view.receipts.length === 0"
            class="muted"
            data-testid="recovery-receipts-none"
          >
            No effect has been recorded yet, so no side effect can be replayed by accident.
          </p>

          <ul v-else class="receipt-list">
            <li
              v-for="receipt in store.view.receipts"
              :key="receipt.id"
              class="receipt"
              :data-testid="`receipt-${receipt.effect_id}`"
            >
              <code data-testid="receipt-operation">{{ receipt.operation }}</code>
              <code class="receipt-effect" data-testid="receipt-effect-id">{{
                receipt.effect_id
              }}</code>
              <span class="badge badge-ok" data-testid="receipt-status">{{ receipt.status }}</span>
              <span class="muted" data-testid="receipt-created">{{
                createdLabel(receipt.created_at)
              }}</span>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="recovery-checkpoints">
          <h3 class="card-title">Checkpoints ({{ store.view.checkpoints.length }})</h3>

          <p
            v-if="store.view.checkpoints.length === 0"
            class="muted"
            data-testid="recovery-checkpoints-none"
          >
            No known-good snapshot has been stored for this run, so there is nothing to roll back
            to.
          </p>

          <ul v-else class="checkpoint-list">
            <li
              v-for="checkpoint in store.view.checkpoints"
              :key="checkpoint.id"
              class="checkpoint"
              :data-testid="`checkpoint-${checkpoint.id}`"
            >
              <code data-testid="checkpoint-id">{{ checkpoint.id }}</code>
              <span
                class="badge"
                :class="`status-${checkpoint.verdict ?? 'unknown'}`"
                data-testid="checkpoint-verdict"
              >
                {{ checkpoint.verdict ?? 'unknown' }}
              </span>
              <span class="muted" data-testid="checkpoint-files">
                {{ checkpoint.file_count }} files · {{ createdLabel(checkpoint.created_at) }}
              </span>
              <button
                class="ghost"
                type="button"
                data-testid="checkpoint-select"
                :disabled="!store.canAct"
                @click="pickCheckpoint(checkpoint.id)"
              >
                Select
              </button>
            </li>
          </ul>
        </section>

        <section class="card panel" data-testid="recovery-actions">
          <h3 class="card-title">Actions</h3>

          <form class="action-form" @submit.prevent="submitRecover">
            <FormField
              label="Reason (required)"
              field-id="recovery-reason"
              hint="Recorded in the recovery journal. The server decides whether the attempt may run."
              :error="recoverReasonError"
            >
              <textarea
                id="recovery-reason"
                v-model="recoverReason"
                rows="3"
                maxlength="1000"
                data-testid="recovery-reason-input"
              />
            </FormField>

            <FormField
              label="Failure class"
              field-id="recovery-class"
              hint="Bounds which retry policy applies. The server still validates the class."
            >
              <select
                id="recovery-class"
                v-model="recoverClass"
                data-testid="recovery-class-select"
              >
                <option v-for="klass in FAILURE_CLASSES" :key="klass" :value="klass">
                  {{ klass }}
                </option>
              </select>
            </FormField>

            <div class="action-row">
              <button
                class="primary"
                type="submit"
                data-testid="recovery-submit"
                :disabled="store.recovering || !store.canAct || store.isTerminal || store.loading"
              >
                {{ store.recovering ? 'Sending…' : 'Plan next attempt' }}
              </button>
              <span v-if="store.isTerminal" class="muted" data-testid="recovery-terminal-note">
                The run reached a terminal state, so no further attempt is offered.
              </span>
            </div>
          </form>

          <form class="action-form" @submit.prevent="submitRollback">
            <FormField
              label="Rollback reason (required)"
              field-id="recovery-rollback-reason"
              hint="Recorded with the restore. The newest checkpoint is used when none is selected."
              :error="rollbackEffectError"
            >
              <textarea
                id="recovery-rollback-reason"
                v-model="rollbackReason"
                rows="2"
                maxlength="1000"
                data-testid="recovery-rollback-reason-input"
              />
            </FormField>

            <div class="action-row">
              <button
                class="primary"
                type="submit"
                data-testid="recovery-rollback-submit"
                :disabled="
                  store.rollingBack ||
                  !store.canAct ||
                  store.view.checkpoints.length === 0 ||
                  store.isTerminal
                "
              >
                {{ store.rollingBack ? 'Restoring…' : 'Roll back to checkpoint' }}
              </button>
              <span
                v-if="store.view.checkpoints.length === 0"
                class="muted"
                data-testid="recovery-no-checkpoint"
              >
                A checkpoint is required before a rollback can be offered.
              </span>
            </div>
          </form>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.panel-blocked {
  border-left: 3px solid var(--color-gold);
}

.blocked-reason {
  margin: 0;
  font-size: 0.86rem;
  color: var(--color-navy);
}

.budget-list,
.receipt-list,
.checkpoint-list,
.timeline {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.budget,
.receipt,
.checkpoint,
.timeline-entry {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 0.65rem;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-surface);
}

.budget-exhausted {
  border-color: var(--color-danger);
}

.timeline-entry {
  flex-direction: column;
  align-items: flex-start;
}

.timeline-reason {
  font-size: 0.85rem;
}

.timeline-meta {
  font-size: 0.75rem;
}

.receipt-effect {
  color: var(--color-text-muted);
}

.grid {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.8rem;
}

.grid th,
.grid td {
  text-align: left;
  padding: 0.35rem 0.5rem;
  border-bottom: 1px solid var(--color-border);
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
}
</style>
