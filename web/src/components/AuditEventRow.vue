<script setup lang="ts">
import { computed } from 'vue';

import type { AuditEvent } from '@/api/types';

const props = defineProps<{ event: AuditEvent }>();

const createdAt = computed(() => new Date(props.event.created_at).toLocaleString());

const payload = computed(() => {
  const value = props.event.payload;
  if (value === undefined) return null;
  return JSON.stringify(value);
});

function target(event: AuditEvent): string {
  if (event.entity_id && event.entity_type) return `${event.entity_type}: ${event.entity_id}`;
  return event.entity_type;
}
</script>

<template>
  <li class="audit-row" :data-testid="`audit-${event.id}`">
    <div class="audit-row-main">
      <span class="audit-action">{{ event.action }}</span>
      <span class="audit-target">{{ target(event) }}</span>
    </div>
    <p v-if="payload" class="audit-payload">{{ payload }}</p>
    <div class="audit-meta">
      <span>{{ createdAt }}</span>
      <span>sequence {{ event.sequence }}</span>
      <span>request {{ event.request_id }}</span>
    </div>
  </li>
</template>

<style scoped>
.audit-row {
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  padding: 0.7rem 0.85rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  background: var(--color-surface);
}

.audit-row-main {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.75rem;
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-navy);
}

.audit-target {
  font-weight: 400;
  font-size: 0.78rem;
  color: var(--color-text-muted);
  word-break: break-all;
}

.audit-payload {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-text);
  background: var(--color-canvas);
  border-radius: var(--radius-sm);
  padding: 0.4rem 0.5rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
}

.audit-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  font-size: 0.72rem;
  color: var(--color-text-muted);
}
</style>
