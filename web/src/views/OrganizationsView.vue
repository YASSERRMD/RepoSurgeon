<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';

import EmptyState from '@/components/EmptyState.vue';
import FormField from '@/components/FormField.vue';
import { useOrganizationsStore } from '@/stores/organizations';
import type { OrganizationRole } from '@/api/types';

const organizations = useOrganizationsStore();
const router = useRouter();

const name = ref('');
const slug = ref('');
const nameError = ref('');
const slugError = ref('');
const formError = ref('');

const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$/;

onMounted(() => {
  void organizations.load();
});

function validate(): boolean {
  nameError.value = '';
  slugError.value = '';
  formError.value = '';

  const trimmedName = name.value.trim();
  const trimmedSlug = slug.value.trim();

  if (trimmedName === '') {
    nameError.value = 'Name is required.';
  } else if (trimmedName.length > 120) {
    nameError.value = 'Name must be 120 characters or fewer.';
  }

  if (trimmedSlug === '') {
    slugError.value = 'Slug is required.';
  } else if (!SLUG_PATTERN.test(trimmedSlug)) {
    slugError.value = 'Use 3–64 characters: lowercase letters, digits or dashes.';
  }

  return nameError.value === '' && slugError.value === '';
}

async function submit(): Promise<void> {
  if (!validate()) return;
  try {
    const created = await organizations.create(name.value.trim(), slug.value.trim());
    name.value = '';
    slug.value = '';
    if (created) {
      await router.push('/projects');
    }
  } catch {
    // The store keeps the API error message for the banner below.
  }
}

function roleTone(role: OrganizationRole): string {
  return `role-${role}`;
}
</script>

<template>
  <div class="organizations">
    <div class="page-head">
      <div>
        <h2 class="page-title">Organizations</h2>
        <p class="muted page-subtitle">
          The tenant boundary for projects. Every project belongs to exactly one organization.
        </p>
      </div>
      <RouterLink class="link-button" to="/projects">Go to projects</RouterLink>
    </div>

    <p v-if="organizations.error" class="banner banner-error" data-testid="org-error">
      <strong>Could not load organizations.</strong> {{ organizations.error }}
      <button class="retry" type="button" data-testid="org-retry" @click="organizations.load()">
        Retry
      </button>
    </p>

    <section class="card" data-testid="create-org-form">
      <h3 class="card-title">Create organization</h3>
      <form novalidate @submit.prevent="submit">
        <div class="form-grid">
          <FormField label="Name" field-id="org-name" :error="nameError">
            <input
              id="org-name"
              v-model="name"
              type="text"
              maxlength="120"
              placeholder="Acme Engineering"
            />
          </FormField>
          <FormField
            label="Slug"
            field-id="org-slug"
            :error="slugError"
            hint="Lowercase letters, digits and dashes — used in URLs."
          >
            <input id="org-slug" v-model="slug" type="text" placeholder="acme-engineering" />
          </FormField>
        </div>
        <button
          class="primary"
          type="submit"
          data-testid="create-org-submit"
          :disabled="organizations.creating"
        >
          {{ organizations.creating ? 'Creating…' : 'Create organization' }}
        </button>
      </form>
    </section>

    <div v-if="organizations.loading" data-testid="org-loading">
      <div class="skeleton" />
      <div class="skeleton" />
    </div>

    <EmptyState
      v-else-if="organizations.items.length === 0"
      title="No organizations yet"
      description="Create one above to start organizing RepoSurgeon projects."
      test-id="empty-organizations"
    />

    <ul v-else class="org-list" data-testid="org-list">
      <li
        v-for="organization in organizations.items"
        :key="organization.id"
        class="org-row card"
        :class="{ 'org-row-active': organization.id === organizations.activeOrganizationId }"
        :data-testid="`org-${organization.id}`"
      >
        <div class="org-identity">
          <span class="org-name">{{ organization.name }}</span>
          <span class="org-slug">{{ organization.slug }}</span>
        </div>
        <div class="org-meta">
          <span class="badge" :class="roleTone(organization.role)">{{ organization.role }}</span>
          <span
            v-if="organization.id === organizations.activeOrganizationId"
            class="badge badge-active"
          >
            active
          </span>
          <button
            class="ghost"
            type="button"
            :data-testid="`select-${organization.id}`"
            :disabled="organization.id === organizations.activeOrganizationId"
            @click="organizations.setActive(organization.id)"
          >
            {{ organization.id === organizations.activeOrganizationId ? 'Selected' : 'Set active' }}
          </button>
        </div>
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
  max-width: 70ch;
}

.link-button {
  border: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  color: var(--color-navy);
  padding: 0.45rem 0.9rem;
  border-radius: var(--radius-sm);
  font-size: 0.82rem;
  font-weight: 600;
  text-decoration: none;
}

.link-button:hover {
  border-color: var(--color-navy);
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

.organizations {
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

.card-title {
  font-size: 1rem;
  margin-bottom: 0.9rem;
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 0.9rem;
  margin-bottom: 1rem;
}

input {
  font: inherit;
  width: 100%;
  padding: 0.45rem 0.6rem;
  border: 1px solid var(--color-gray-line);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
}

input:focus {
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

.org-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.org-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
  padding: 0.9rem 1.1rem;
}

.org-row-active {
  border-color: var(--color-gold);
  background: linear-gradient(0deg, var(--color-gold-soft), var(--color-gold-soft));
}

.org-identity {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.org-name {
  font-weight: 650;
  color: var(--color-navy);
}

.org-slug {
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.org-meta {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.role-owner {
  background: var(--color-gold-soft);
  color: var(--color-navy);
  border: 1px solid rgb(201 162 39 / 35%);
}

.role-admin {
  background: #eef2ff;
  color: #3730a3;
}

.role-member {
  background: #f3f4f6;
  color: var(--color-text);
}

.role-viewer {
  background: #f3f4f6;
  color: var(--color-text-muted);
}

.badge-active {
  background: var(--color-ok-soft);
  color: var(--color-ok);
}

.skeleton {
  height: 3.4rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-gray-line);
  background: linear-gradient(90deg, #f3f4f6, #e5e7eb, #f3f4f6);
  background-size: 200% 100%;
}

.skeleton + .skeleton {
  margin-top: 0.75rem;
}
</style>
