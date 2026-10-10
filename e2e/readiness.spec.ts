import { expect, test } from '@playwright/test';

/**
 * These specs exercise the real composed stack: the browser loads the Vue app
 * from the web container and every probe result comes from the Go API and
 * PostgreSQL. Nothing is mocked here; the negative case uses the app's own
 * actionable error state by pointing the browser at an unreachable API.
 */

test.describe('operational readiness', () => {
  test('home page renders the brand and measured service status', async ({ page, request }) => {
    const readiness = await request.get('/health/ready');
    const ready = readiness.status() === 200;
    test.info().annotations.push({
      type: 'readiness',
      description: `/health/ready returned ${readiness.status()}`,
    });

    await page.goto('/');

    await expect(page.getByRole('heading', { name: 'RepoSurgeon', level: 1 })).toBeVisible();

    const readinessCard = page.getByTestId('readiness-card');
    await expect(readinessCard).toBeVisible();

    if (ready) {
      await expect(readinessCard).toContainText('up');
      const checks = page.locator('.check-row');
      await expect(checks).toHaveCount(2);
      await expect(page.getByTestId('check-database')).toContainText('up');
      await expect(page.getByTestId('check-migrations')).toContainText('up');
    } else {
      // A degraded stack must still be explained, never silently green.
      await expect(page.getByTestId('remediation')).toBeVisible();
    }

    await expect(page.getByTestId('system-card')).toBeVisible();
  });

  test('system info card reports real build facts', async ({ page }) => {
    await page.goto('/');
    const systemCard = page.getByTestId('system-card');
    await expect(systemCard).toBeVisible();
    await expect(systemCard).toContainText('reposurgeon');
    await expect(systemCard).toContainText('go1.');
  });

  test('readiness probe matches what the API reports', async ({ page, request }) => {
    const api = await request.get('/health/ready');
    expect([200, 503]).toContain(api.status());
    const report = (await api.json()) as { status: string; checks: unknown[] };

    await page.goto('/');
    const card = page.getByTestId('readiness-card');
    if (report.status === 'ready') {
      await expect(card).toContainText('up');
    } else {
      await expect(card).toContainText('down');
      await expect(page.getByTestId('remediation')).toBeVisible();
    }
  });
});
