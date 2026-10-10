import { expect, test } from '@playwright/test';

/** Responsive behaviour of the operational landing view. */
test.describe('responsive layout', () => {
  test('status grid stacks on a narrow viewport', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/');

    await expect(page.getByRole('heading', { name: 'RepoSurgeon', level: 1 })).toBeVisible();

    const grid = page.locator('.grid');
    await expect(grid).toBeVisible();
    const columns = await grid.evaluate(
      (el) => getComputedStyle(el).gridTemplateColumns.split(' ').length,
    );
    expect(columns).toBeGreaterThanOrEqual(1);
  });

  test('header and footer remain usable at mobile width', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/');

    await expect(page.getByTestId('footer-status')).toBeVisible();

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth + 1,
    );
    expect(overflow).toBe(true);
  });
});
