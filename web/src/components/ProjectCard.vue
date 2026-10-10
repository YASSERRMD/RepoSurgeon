<script setup lang="ts">
import type { Project } from '@/api/types';

defineProps<{
  project: Project;
}>();

const emit = defineEmits<{
  (event: 'open', projectId: string): void;
}>();

function updatedAt(project: Project): string {
  return new Date(project.updated_at).toLocaleString();
}
</script>

<template>
  <article class="project-card card" :data-testid="`project-${project.id}`">
    <header class="project-card-head">
      <h3 class="project-name">{{ project.name }}</h3>
      <span class="badge badge-version">v{{ project.version }}</span>
    </header>
    <p v-if="project.description" class="project-description">{{ project.description }}</p>
    <p v-else class="project-description muted">No description yet.</p>
    <footer class="project-card-foot">
      <span class="muted">Updated {{ updatedAt(project) }}</span>
      <button class="project-open" type="button" @click="emit('open', project.id)">Open</button>
    </footer>
  </article>
</template>

<style scoped>
.project-card {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.project-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.project-name {
  font-size: 1rem;
  word-break: break-word;
}

.badge-version {
  background: #f3f4f6;
  color: var(--color-navy);
  border: 1px solid var(--color-gray-line);
}

.project-description {
  margin: 0;
  font-size: 0.85rem;
  color: var(--color-text);
  line-height: 1.5;
  flex: 1;
}

.project-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  font-size: 0.76rem;
  border-top: 1px dashed var(--color-gray-line);
  padding-top: 0.6rem;
}

.project-open {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.35rem 0.8rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.project-open:hover {
  background: var(--color-navy-soft);
}
</style>
