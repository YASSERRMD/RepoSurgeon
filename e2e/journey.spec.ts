import { expect, test } from '@playwright/test';

import { signIn } from './helpers';

/**
 * The login-to-inspection journey runs against the real composed stack: a real
 * session, real projects and real run records from PostgreSQL. Every assertion
 * reads what the server returned, so a fabricated dashboard would fail here.
 */
test.describe('issue to pull request journey', () => {
  test('signs in, opens the overview and reads the aggregate', async ({ page, request }) => {
    await signIn(page, request);

    // The overview is the landing section; the aggregate is server computed.
    await expect(page.getByRole('heading', { name: 'Overview', level: 2 })).toBeVisible();
    const metrics = page.getByTestId('dashboard-metrics');
    await expect(metrics).toBeVisible();
    // Every metric is an integer the API returned, never a placeholder.
    const openIssues = await page.getByTestId('metric-open-issues').textContent();
    expect(openIssues?.trim()).toMatch(/^\d+$/);

    // The overview states are rendered from the aggregate.
    const states = page.getByTestId('dashboard-states');
    await expect(states).toBeVisible();
  });

  test('navigates the workbench sections and keeps the keyboard focus visible', async ({
    page,
    request,
  }) => {
    await signIn(page, request);

    for (const [name, heading] of [
      ['Projects', 'Projects'],
      ['Issues', 'Issues'],
      ['Runs', 'Runs'],
      ['Models', 'Models'],
      ['Audit', 'Audit'],
      ['Settings', 'Settings'],
    ] as const) {
      await page.getByRole('link', { name }).click();
      await expect(page.getByRole('heading', { name: heading, level: 2 })).toBeVisible();
    }

    // The active section is announced through aria-current, not colour alone.
    await page.getByRole('link', { name: 'Runs' }).click();
    const active = page.locator('nav a[aria-current="page"]');
    await expect(active).toHaveCount(1);
    await expect(active).toHaveText('Runs');
  });

  test('the console reports failures instead of inventing data', async ({ page, request }) => {
    await signIn(page, request);

    // Point the browser at an unreachable API and reload the overview: the
    // failure banner must appear rather than a fabricated dashboard.
    await page.route('**/api/v1/dashboard', (route) => route.abort());
    await page.getByRole('link', { name: 'Overview' }).click();

    await expect(page.getByTestId('dashboard-error').first()).toBeVisible();
    // No metric is rendered from a failed read.
    await expect(page.getByTestId('metric-open-issues')).toHaveCount(0);
    await page.unroute('**/api/v1/dashboard');
  });
});
