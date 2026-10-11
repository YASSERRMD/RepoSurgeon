import { expect, test } from '@playwright/test';

/**
 * M02 browser journey against the composed stack: bootstrap the first account,
 * log in, confirm protected routes, log out and confirm the session is gone.
 * The stack keeps the session in an HttpOnly cookie, so the assertions here
 * are about what the user can actually do.
 */
test.describe('authentication', () => {
  const password = 'e2e-login-password-1';

  test('login, protected routes and logout', async ({ page, request }) => {
    const readiness = await request.get('/health/ready');
    test.skip(readiness.status() !== 200, 'stack is not ready; skipping auth journey');

    const email = `e2e-${Date.now()}@example.test`;

    // Bootstrap the first account on a fresh stack.
    const bootstrap = await request.post('/api/v1/auth/bootstrap', {
      data: { email, password, organization_name: 'E2E Org', display_name: 'E2E' },
    });
    test.skip(bootstrap.status() !== 201, 'an account already exists; run against a fresh stack');

    // Visiting a protected route redirects to the login page.
    await page.goto('/members');
    await expect(page).toHaveURL(/\/login/, { timeout: 15000 });

    // Wrong credentials are rejected without revealing whether the account exists.
    await page.locator('input[type="email"]').first().fill(email);
    await page.locator('input[type="password"]').first().fill('totally-wrong-password');
    await page.getByRole('button', { name: /sign in|log in/i }).click();
    await expect(page.getByTestId('login-error')).toBeVisible();

    // Correct credentials land on the originally requested page.
    await page.locator('input[type="password"]').first().fill(password);
    await page.getByRole('button', { name: /sign in|log in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 });

    // The members view is reachable and shows the authenticated user.
    await page.goto('/members');
    await expect(page.getByText(email).first()).toBeVisible({ timeout: 15000 });

    // Signing out returns the app to an anonymous state.
    await page.getByRole('button', { name: /sign out|log out/i }).click();
    await expect(page).toHaveURL(/\/login/, { timeout: 15000 });

    // A protected route is no longer reachable.
    await page.goto('/members');
    await expect(page).toHaveURL(/\/login/, { timeout: 15000 });
  });
});
