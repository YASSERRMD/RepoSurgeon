<script setup lang="ts">
import type { CheckStatus } from '@/api/types';

defineProps<{
  title: string;
  status?: CheckStatus | 'unknown';
  detail?: string;
  testId?: string;
}>();

const statusText: Record<string, string> = {
  up: 'up',
  down: 'down',
  unknown: 'unknown',
};
</script>

<template>
  <section class="status-card card" :data-testid="testId">
    <header class="status-card-head">
      <h2>{{ title }}</h2>
      <span class="pill" :class="`pill-${status ?? 'unknown'}`">
        {{ statusText[status ?? 'unknown'] }}
      </span>
    </header>
    <p v-if="detail" class="status-card-detail">{{ detail }}</p>
    <slot />
  </section>
</template>

<style scoped>
.status-card {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.status-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.status-card-head h2 {
  font-size: 1rem;
}

.status-card-detail {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.85rem;
  line-height: 1.5;
}

.pill {
  padding: 0.15rem 0.6rem;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.pill-up {
  background: var(--color-ok-soft);
  color: var(--color-ok);
}

.pill-down {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}

.pill-unknown {
  background: #f3f4f6;
  color: var(--color-text-muted);
}
</style>
