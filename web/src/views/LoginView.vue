<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import FormField from '@/components/FormField.vue';
import { ApiError } from '@/api/client';
import { useAuthStore } from '@/stores/auth';

const auth = useAuthStore();
const route = useRoute();
const router = useRouter();

const email = ref('');
const password = ref('');
const emailError = ref('');
const passwordError = ref('');
const errorCode = ref<string | null>(null);

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const errorMessageForCode: Record<string, string> = {
  invalid_credentials: 'Email or password is incorrect.',
  rate_limited: 'Too many sign-in attempts. Wait a moment and try again.',
  unauthenticated: 'Your session is no longer valid — sign in again.',
  network_error: 'Could not reach the RepoSurgeon API.',
  validation_failed: 'The server rejected those credentials. Check both fields.',
};

const submitting = computed(() => auth.loading);
const formError = computed<string | null>(() =>
  errorCode.value === null ? null : (errorMessageForCode[errorCode.value] ?? 'Sign-in failed.'),
);

/**
 * nextPath only follows a same-origin path, so a crafted `next` query value
 * can never redirect the browser to another site after sign-in.
 */
function nextPath(): string {
  const next = route.query.next;
  if (typeof next !== 'string') return '/';
  if (!next.startsWith('/') || next.startsWith('//')) return '/';
  return next;
}

function validate(): boolean {
  emailError.value = '';
  passwordError.value = '';
  errorCode.value = null;

  const trimmedEmail = email.value.trim();
  if (trimmedEmail === '') {
    emailError.value = 'Email is required.';
  } else if (!EMAIL_PATTERN.test(trimmedEmail)) {
    emailError.value = 'Enter a valid email address.';
  }

  if (password.value.length < 12) {
    passwordError.value = 'Password must be at least 12 characters.';
  }

  return emailError.value === '' && passwordError.value === '';
}

async function submit(): Promise<void> {
  if (!validate()) return;
  try {
    await auth.login(email.value.trim(), password.value);
    password.value = '';
    await router.push(nextPath());
  } catch (cause) {
    errorCode.value = cause instanceof ApiError ? cause.code : 'unexpected_error';
    emailError.value = '';
    passwordError.value = '';
  }
}
</script>

<template>
  <div class="login">
    <section class="login-card card">
      <h2 class="login-title">Sign in</h2>
      <p class="muted login-subtitle">
        Credentials are checked server-side by the RepoSurgeon API; nothing is validated locally
        beyond the form format below.
      </p>

      <p
        v-if="formError"
        class="banner banner-error"
        data-testid="login-error"
        :data-code="errorCode ?? undefined"
      >
        <strong>Sign-in failed.</strong> {{ formError }}
      </p>

      <form novalidate @submit.prevent="submit">
        <FormField label="Email" field-id="login-email" :error="emailError">
          <input
            id="login-email"
            v-model="email"
            type="email"
            autocomplete="username"
            maxlength="320"
            placeholder="developer@example.com"
            data-testid="login-email"
          />
        </FormField>

        <FormField
          label="Password"
          field-id="login-password"
          :error="passwordError"
          hint="At least 12 characters."
        >
          <input
            id="login-password"
            v-model="password"
            type="password"
            autocomplete="current-password"
            maxlength="256"
            placeholder="••••••••••••"
            data-testid="login-password"
          />
        </FormField>

        <button class="primary" type="submit" data-testid="login-submit" :disabled="submitting">
          {{ submitting ? 'Signing in…' : 'Sign in' }}
        </button>
      </form>
    </section>
  </div>
</template>

<style scoped>
.login {
  display: flex;
  justify-content: center;
}

.login-card {
  width: min(26rem, 100%);
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.login-title {
  font-size: 1.35rem;
}

.login-subtitle {
  margin: 0;
  font-size: 0.86rem;
  max-width: 46ch;
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
}

form {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
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
</style>
