<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { RouterLink, useRouter } from 'vue-router';

import { useHealthStore } from '@/stores/health';
import { useOrganizationsStore } from '@/stores/organizations';
import { useAuthStore } from '@/stores/auth';

const health = useHealthStore();
const organizations = useOrganizationsStore();
const auth = useAuthStore();
const router = useRouter();

const navLinks = [
  { to: '/', label: 'Status' },
  { to: '/organizations', label: 'Organizations' },
  { to: '/projects', label: 'Projects' },
  { to: '/members', label: 'Members' },
  { to: '/credentials', label: 'Credentials' },
  { to: '/approvals', label: 'Approvals' },
  { to: '/tools', label: 'Tools' },
];

const userEmail = computed(() => auth.user?.email ?? '');

// The header shows the active organization; resolve its name once on boot.
onMounted(() => {
  if (!organizations.loaded) void organizations.load();
});

async function signOut(): Promise<void> {
  await auth.logout();
  // The session is gone locally; the guard sends the browser back to /login.
  if (!auth.isAuthenticated) await router.push('/login');
}

const statusLabels: Record<string, string> = {
  ready: 'All systems operational',
  unavailable: 'Degraded — action required',
  loading: 'Checking dependencies…',
  error: 'Cannot reach the API',
  idle: 'Initialising',
};
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true">RS</span>
        <div>
          <h1 class="brand-name">RepoSurgeon</h1>
          <p class="brand-subtitle">Autonomous software engineering workspace</p>
        </div>
      </div>
      <div class="header-meta">
        <nav class="app-nav" aria-label="Primary">
          <RouterLink v-for="link in navLinks" :key="link.to" :to="link.to" class="nav-link">
            {{ link.label }}
          </RouterLink>
        </nav>
        <span
          v-if="organizations.activeOrganization"
          class="badge badge-active-org"
          data-testid="active-org"
        >
          {{ organizations.activeOrganization.name }}
        </span>
        <span v-if="health.system" class="badge badge-env">{{ health.system.environment }}</span>
        <span v-if="health.system" class="version">v{{ health.system.version }}</span>
        <span v-if="auth.isAuthenticated" class="badge badge-user" data-testid="current-user">
          {{ userEmail }}
        </span>
        <button
          v-if="auth.isAuthenticated"
          class="signout"
          type="button"
          data-testid="sign-out"
          :disabled="auth.loading"
          @click="signOut()"
        >
          Sign out
        </button>
      </div>
    </header>

    <main class="app-main">
      <RouterView />
    </main>

    <footer class="app-footer">
      <span data-testid="footer-status">{{ statusLabels[health.status] }}</span>
      <span v-if="health.lastCheckedAt">
        last checked {{ new Date(health.lastCheckedAt).toLocaleTimeString() }}
      </span>
    </footer>
  </div>
</template>

<style scoped>
.app-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.75rem;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-gray-line);
}

.brand {
  display: flex;
  align-items: center;
  gap: 0.85rem;
}

.brand-mark {
  display: grid;
  place-items: center;
  width: 2.4rem;
  height: 2.4rem;
  border-radius: var(--radius-sm);
  background: var(--color-navy);
  color: var(--color-gold);
  font-weight: 800;
  letter-spacing: 0.04em;
}

.brand-name {
  font-size: 1.15rem;
}

.brand-subtitle {
  margin: 0.1rem 0 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.header-meta {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.app-nav {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  border: 1px solid var(--color-gray-line);
  border-radius: 999px;
  padding: 0.15rem;
}

.nav-link {
  padding: 0.3rem 0.8rem;
  border-radius: 999px;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-navy);
  text-decoration: none;
}

.nav-link:hover {
  background: var(--color-canvas);
}

.nav-link.router-link-active {
  background: var(--color-navy);
  color: #fff;
}

.badge-active-org {
  background: var(--color-navy);
  color: #fff;
  border: 1px solid var(--color-navy);
  max-width: 14rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.badge-user {
  background: var(--color-surface);
  color: var(--color-navy);
  border: 1px solid var(--color-gray-line);
  max-width: 16rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.signout {
  border: 1px solid var(--color-navy);
  background: var(--color-surface);
  color: var(--color-navy);
  padding: 0.3rem 0.8rem;
  border-radius: var(--radius-sm);
  font-size: 0.78rem;
  font-weight: 600;
}

.signout:hover:not(:disabled) {
  background: var(--color-navy);
  color: #fff;
}

.version {
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

.app-main {
  flex: 1;
  width: min(1120px, 100%);
  margin: 0 auto;
  padding: 2rem 1.75rem;
}

.app-footer {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.9rem 1.75rem;
  border-top: 1px solid var(--color-gray-line);
  background: var(--color-surface);
  font-size: 0.78rem;
  color: var(--color-text-muted);
}

@media (max-width: 640px) {
  .app-header,
  .app-main,
  .app-footer {
    padding-left: 1rem;
    padding-right: 1rem;
  }

  /* Stack the header on narrow screens so navigation never overflows. */
  .app-header {
    flex-wrap: wrap;
    gap: 0.75rem;
  }

  .brand-subtitle {
    display: none;
  }

  .header-meta {
    width: 100%;
    justify-content: space-between;
  }

  .app-nav {
    max-width: 100%;
    overflow-x: auto;
  }

  .app-footer {
    flex-wrap: wrap;
    gap: 0.35rem;
  }
}
</style>
