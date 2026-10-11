import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

import { ApiError, createHealthClient, newIdempotencyKey, type HealthClient } from '@/api/client';
import type {
  Approval,
  ApprovalDecision,
  ApprovalDecisionChoice,
  ApprovalListResponse,
  ToolDescriptor,
  ToolListResponse,
} from '@/api/types';
import { errorMessage } from '@/stores/organizations';

export interface ApprovalRejection {
  /** message is the server's explanation of a decision it refused. */
  message: string;
}

/**
 * The tools store owns the two halves of the policy surface: the tool catalog
 * (what may be called and under which permission) and the approval inbox (who
 * authorises a high-risk action). Nothing in this store executes a tool or
 * predicts a decision — every state change comes from a server response.
 */
export const useToolsStore = defineStore('tools', () => {
  const client: HealthClient = createHealthClient();
  const tools = ref<ToolDescriptor[]>([]);
  const items = ref<Approval[]>([]);
  const loading = ref(false);
  const loadingTools = ref(false);
  const decidingApprovalId = ref<string | null>(null);
  const error = ref<string | null>(null);
  const toolsError = ref<string | null>(null);
  const conflict = ref<ApprovalRejection | null>(null);

  /** pendingCount counts the requests that are still waiting for a human. */
  const pendingCount = computed(
    () => items.value.filter((item) => item.state === 'pending').length,
  );

  /** fetchInbox replaces the list with the server's answer; callers handle errors. */
  async function fetchInbox(): Promise<void> {
    const page: ApprovalListResponse = await client.listApprovals();
    items.value = page.items;
  }

  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    conflict.value = null;
    try {
      await fetchInbox();
    } catch (cause) {
      items.value = [];
      error.value = errorMessage(cause);
    } finally {
      loading.value = false;
    }
  }

  async function loadTools(): Promise<void> {
    loadingTools.value = true;
    toolsError.value = null;
    try {
      const page: ToolListResponse = await client.listTools();
      tools.value = page.items;
    } catch (cause) {
      tools.value = [];
      toolsError.value = errorMessage(cause);
    } finally {
      loadingTools.value = false;
    }
  }

  /**
   * decide posts a human decision and adopts the server's approval verbatim.
   * A 409 means the request left the pending state under someone else's hand,
   * so the inbox is reloaded instead of guessing what happened.
   */
  async function decide(
    approvalId: string,
    decision: ApprovalDecisionChoice,
    reason = '',
  ): Promise<Approval> {
    decidingApprovalId.value = approvalId;
    error.value = null;
    conflict.value = null;
    try {
      const trimmed = reason.trim();
      // exactOptionalPropertyTypes: an empty reason is omitted, never sent as null.
      const body: ApprovalDecision = trimmed === '' ? { decision } : { decision, reason: trimmed };
      const decided = await client.decideApproval(approvalId, body, newIdempotencyKey());
      items.value = items.value.map((item) => (item.id === decided.id ? decided : item));
      return decided;
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        conflict.value = { message: cause.message };
        try {
          await fetchInbox();
        } catch {
          // The banner already explains the rejection; the list keeps its rows.
        }
      } else {
        error.value = errorMessage(cause);
      }
      throw cause;
    } finally {
      decidingApprovalId.value = null;
    }
  }

  function approve(approvalId: string, reason = ''): Promise<Approval> {
    return decide(approvalId, 'approve', reason);
  }

  function reject(approvalId: string, reason = ''): Promise<Approval> {
    return decide(approvalId, 'reject', reason);
  }

  return {
    client,
    tools,
    items,
    loading,
    loadingTools,
    decidingApprovalId,
    error,
    toolsError,
    conflict,
    pendingCount,
    load,
    loadTools,
    decide,
    approve,
    reject,
  };
});
