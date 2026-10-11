import { expect, test, type Page, type APIRequestContext } from '@playwright/test';

/**
 * Boots the first account on a fresh stack and signs in through the UI so the
 * browser holds a real session cookie and the CSRF token for mutations.
 *
 * Returns the credentials used, so specs can assert against them.
 */
export async function signIn(page: Page, request: APIRequestContext): Promise<{
  email: string;
  password: string;
}> {
  const password = 'e2e-journey-password-1';
  const email = `e2e-${Date.now()}@example.test`;

  const bootstrap = await request.post('/api/v1/auth/bootstrap', {
    data: { email, password, organization_name: 'E2E Org', display_name: 'E2E' },
  });
  if (bootstrap.status() !== 201) {
    // The stack already has an account, so this journey cannot control its
    // credentials. CI always runs against a fresh database volume.
    test.skip(true, `bootstrap returned ${bootstrap.status()}; run against a fresh stack`);
  }

  await page.goto('/login');
  await page.locator('input[type="email"]').first().fill(email);
  await page.locator('input[type="password"]').first().fill(password);
  await page.getByRole('button', { name: /sign in/i }).click();
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 });
  await expect(page.getByTestId('login-error')).toBeHidden();
  return { email, password };
}
