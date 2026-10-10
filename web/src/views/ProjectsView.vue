<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import ProjectCard from '@/components/ProjectCard.vue';
import { useOrganizationsStore } from '@/stores/organizations';
import { useProjectsStore } from '@/stores/projects';

const organizations = useOrganizationsStore();
const projects = useProjectsStore();
const router = useRouter();

const search = ref('');
const formOpen = ref(false);
const projectName = ref('');
const projectDescription = ref('');
const nameError = ref('');

const activeOrganizationId = computed(() => organizations.activeOrganizationId);
const activeOrganization = computed(() => organizations.activeOrganization);
const needingOrganization = computed(
  () => activeOrganizationId.value === null && !organizations.loading && organizations.loaded,
);

onMounted(() => {
  if (!organizations.loaded) void organizations.load();
  if (activeOrganizationId.value !== null) {
    void projects.load(activeOrganizationId.value, { q: search.value });
  }
});

function applySearch(): void {
  if (activeOrganizationId.value === null) return;
  void projects.load(activeOrganizationId.value, { q: search.value });
}

function openProject(projectId: string): void {
  void router.push(`/projects/${projectId}`);
}

function toggleForm(): void {
  formOpen.value = !formOpen.value;
  nameError.value = '';
}

async function submitProject(): Promise<void> {
  const trimmed = projectName.value.trim();
  if (trimmed === '') {
    nameError.value = 'Name is required.';
    return;
  }
  if (activeOrganizationId.value === null) return;
  try {
    const created = await projects.create({
      organization_id: activeOrganizationId.value,
      name: trimmed,
      description: projectDescription.value.trim(),
    });
    formOpen.value = false;
    projectName.value = '';
    projectDescription.value = '';
    openProject(created.id);
  } catch {
    // The store exposes the API failure through projects.error.
  }
}
</script>

<template>
  <div class="projects">
    <div class="page-head">
      <div>
        <h2 class="page-title">Projects</h2>
        <p class="muted page-subtitle">
          <template v-if="activeOrganization">
            Projects in <strong>{{ activeOrganization.name }}</strong
            >.
          </template>
          <template v-else>Select an organization to see its projects.</template>
        </p>
      </div>
      <button
        v-if="activeOrganizationId !== null"
        class="primary"
        type="button"
        data-testid="new-project-button"
        @click="toggleForm"
      >
        {{ formOpen ? 'Close form' : 'New project' }}
      </button>
    </div>

    <EmptyState
      v-if="needingOrganization"
      title="No active organization"
      description="Projects live inside an organization. Create one or pick an active organization first."
      test-id="no-active-organization"
    >
      <template #action>
        <RouterLink class="link-button" to="/organizations">Manage organizations</RouterLink>
      </template>
    </EmptyState>

    <template v-else>
      <form class="search" @submit.prevent="applySearch">
        <input
          v-model="search"
          type="search"
          placeholder="Filter by project name"
          aria-label="Filter projects by name"
          data-testid="project-search"
        />
        <button class="ghost" type="submit" data-testid="project-search-submit">Search</button>
      </form>

      <section v-if="formOpen" class="card" data-testid="create-project-form">
        <h3 class="card-title">New project</h3>
        <form novalidate @submit.prevent="submitProject">
          <FormField label="Name" field-id="project-name" :error="nameError">
            <input
              id="project-name"
              v-model="projectName"
              type="text"
              maxlength="120"
              placeholder="billing-service"
            />
          </FormField>
          <FormField label="Description" field-id="project-description">
            <textarea
              id="project-description"
              v-model="projectDescription"
              rows="3"
              maxlength="2000"
              placeholder="Payments and invoicing"
            />
          </FormField>
          <button
            class="primary"
            type="submit"
            data-testid="create-project-submit"
            :disabled="projects.creating"
          >
            {{ projects.creating ? 'Creating…' : 'Create project' }}
          </button>
        </form>
      </section>

      <p v-if="projects.error" class="banner banner-error" data-testid="projects-error">
        <strong>Could not load projects.</strong> {{ projects.error }}
        <button class="retry" type="button" data-testid="projects-retry" @click="applySearch">
          Retry
        </button>
      </p>

      <div v-if="projects.loading" data-testid="projects-loading">
        <div class="skeleton" />
        <div class="skeleton" />
      </div>

      <EmptyState
        v-else-if="projects.items.length === 0"
        title="No projects yet"
        description="Create a project to connect a repository to an autonomous engineering run."
        test-id="empty-projects"
      />

      <div v-else class="project-grid" data-testid="project-grid">
        <ProjectCard
          v-for="project in projects.items"
          :key="project.id"
          :project="project"
          @open="openProject"
        />
      </div>

      <div v-if="projects.nextCursor !== null" class="load-more">
        <button
          class="ghost"
          type="button"
          data-testid="projects-load-more"
          :disabled="projects.loadingMore"
          @click="projects.loadMore()"
        >
          {{ projects.loadingMore ? 'Loading…' : 'Load more' }}
        </button>
      </div>
    </template>
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
  max-width: 70ch;
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

.link-button {
  border: 1px solid var(--color-navy);
  background: var(--color-navy);
  color: #fff;
  padding: 0.45rem 0.9rem;
  border-radius: var(--radius-sm);
  font-size: 0.82rem;
  font-weight: 600;
  text-decoration: none;
}

.projects {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.search {
  display: flex;
  gap: 0.5rem;
}

.search input {
  font: inherit;
  flex: 1;
  max-width: 32rem;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-card);
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}

.card-title {
  font-size: 1rem;
}

input,
textarea {
  font: inherit;
  width: 100%;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
}

input:focus,
textarea:focus {
  outline: 2px solid var(--color-gold);
  outline-offset: 1px;
}

.banner {
  margin: 0;
  padding: 0.85rem 1rem;
  border-radius: var(--radius-sm);
  font-size: 0.9rem;
  line-height: 1.5;
}

.banner-error {
  background: var(--color-danger-soft);
  border: 1px solid rgb(180 35 24 / 30%);
  color: var(--color-danger);
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
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

.project-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1rem;
  align-items: stretch;
}

.load-more {
  display: flex;
  justify-content: center;
}

.skeleton {
  height: 8rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
  margin-bottom: 1rem;
}
</style>
