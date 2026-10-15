<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { RouterLink, useRouter } from 'vue-router';

import { useHealthStore } from '@/stores/health';
import { useOrganizationsStore } from '@/stores/organizations';
import { useAuthStore } from '@/stores/auth';
import { useRoute } from 'vue-router';

const health = useHealthStore();
const organizations = useOrganizationsStore();
const auth = useAuthStore();
const router = useRouter();
const route = useRoute();

/**
 * primaryNav is the workbench navigation of record. Every entry points at a
 * real route backed by a real endpoint, and the permission gate is what the API
 * enforces too: a link is hidden when the caller may not use the section, and
 * the API independently refuses the same request.
 */
const primaryNav = [
  { to: '/', label: 'Overview', permission: 'project.read' },
  { to: '/projects', label: 'Projects', permission: 'project.read' },
  { to: '/issues', label: 'Issues', permission: 'project.read' },
  { to: '/runs', label: 'Runs', permission: 'project.read' },
  { to: '/workspaces', label: 'Workspaces', permission: 'project.read' },
  { to: '/models', label: 'Models', permission: 'project.read' },
  { to: '/approvals', label: 'Approvals', permission: 'approval.decide' },
  { to: '/audit', label: 'Audit', permission: 'project.read' },
  { to: '/settings', label: 'Settings', permission: 'project.read' },
];

const adminNav = [
  { to: '/organizations', label: 'Organizations', permission: 'member.admin' },
  { to: '/members', label: 'Members', permission: 'member.admin' },
  { to: '/credentials', label: 'Credentials', permission: 'credential.admin' },
  { to: '/tools', label: 'Tools', permission: 'project.read' },
];

const userEmail = computed(() => auth.user?.email ?? '');

/** can is the permission gate the backend enforces too. */
const can = (permission: string): boolean => auth.can(permission as never);

const visibleNav = computed(() => primaryNav.filter((link) => can(link.permission)));
const visibleAdminNav = computed(() => adminNav.filter((link) => can(link.permission)));
const activeSection = computed(() => String(route.path));

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
          <RouterLink
            v-for="link in visibleNav"
            :key="link.to"
            :to="link.to"
            class="nav-link"
            :aria-current="activeSection === link.to ? 'page' : undefined"
          >
            {{ link.label }}
          </RouterLink>
        </nav>
        <nav
          v-if="visibleAdminNav.length > 0"
          class="app-nav app-nav-admin"
          aria-label="Administration"
        >
          <RouterLink
            v-for="link in visibleAdminNav"
            :key="link.to"
            :to="link.to"
            class="nav-link"
            :aria-current="activeSection === link.to ? 'page' : undefined"
          >
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

/* Keyboard focus stays visible everywhere: the ring uses the gold accent on
   navy so it is distinguishable against both light and dark surfaces. */
.nav-link:focus-visible,
.signout:focus-visible {
  outline: 2px solid var(--color-gold);
  outline-offset: 2px;
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

  /* The two navigation groups wrap into their own rows on a phone, so the
     workbench links stay reachable without horizontal scrolling. */
  .header-meta {
    flex-wrap: wrap;
    gap: 0.5rem;
    width: 100%;
  }

  .app-nav,
  .app-nav-admin {
    flex-wrap: wrap;
    border-radius: var(--radius-sm);
  }

  .badge-active-org,
  .badge-user {
    max-width: 100%;
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

<style scoped>
/* Tablet and wide-desktop layouts keep the header on one row. */
@media (min-width: 641px) and (max-width: 1024px) {
  .app-header {
    flex-wrap: wrap;
    gap: 0.75rem;
  }

  .header-meta {
    flex-wrap: wrap;
    gap: 0.5rem;
  }
}
</style>
