<script setup lang="ts">
defineProps<{
  title: string;
  description: string;
  testId?: string;
}>();
</script>

<template>
  <section class="empty-state card" :data-testid="testId">
    <h2 class="empty-title">{{ title }}</h2>
    <p class="empty-description">{{ description }}</p>
    <div v-if="$slots.action" class="empty-action">
      <slot name="action" />
    </div>
  </section>
</template>

<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  align-items: flex-start;
  border-style: dashed;
}

.empty-title {
  font-size: 1.05rem;
}

.empty-description {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.9rem;
  max-width: 60ch;
}
</style>
