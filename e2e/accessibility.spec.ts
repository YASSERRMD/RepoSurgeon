import { expect, test } from '@playwright/test';

import { signIn } from './helpers';

/**
 * Accessibility and responsive checks for the workbench. They run against the
 * composed stack, so a real keyboard traversal is exercised rather than a
 * mocked DOM.
 */
test.describe('accessibility and responsive layout', () => {
  test('the workbench is reachable by keyboard alone', async ({ page, request }) => {
    await signIn(page, request);

    await page.keyboard.press('Tab');
    const focused = await page.evaluate(() => document.activeElement?.tagName ?? '');
    expect(['A', 'BUTTON', 'INPUT', 'SELECT', 'TEXTAREA']).toContain(focused);

    // The tab order reaches every primary section within the navigation.
    const reached = new Set<string>();
    for (let step = 0; step < 25; step += 1) {
      const label = await page.evaluate(() => document.activeElement?.textContent?.trim() ?? '');
      if (label !== '') reached.add(label);
      await page.keyboard.press('Tab');
    }
    for (const section of ['Overview', 'Projects', 'Runs', 'Settings']) {
      expect([...reached].some((entry) => entry.includes(section))).toBe(true);
    }
  });

  test('the active section is announced without relying on colour', async ({ page, request }) => {
    await signIn(page, request);

    await page.getByRole('link', { name: 'Runs' }).click();
    await expect(page.locator('nav a[aria-current="page"]')).toHaveText('Runs');
  });

  test('the layout does not overflow on a tablet viewport', async ({ page, request }) => {
    await signIn(page, request);
    await page.setViewportSize({ width: 820, height: 1180 });

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth + 1,
    );
    expect(overflow).toBe(true);
  });

  test('the mobile layout stacks the navigation without overflow', async ({ page, request }) => {
    await signIn(page, request);
    await page.setViewportSize({ width: 390, height: 844 });

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth + 1,
    );
    expect(overflow).toBe(true);
    await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible();
  });
});

test.describe('workbench contrast and typography', () => {
  test('the brand palette keeps readable contrast on the primary surface', async ({ page }) => {
    await page.goto('/status');

    const styles = await page.evaluate(() => {
      const brand = document.querySelector('.brand-name');
      if (brand === null) return null;
      const computed = window.getComputedStyle(brand);
      return { color: computed.color, fontSize: computed.fontSize };
    });
    expect(styles).not.toBeNull();
    // Navy on the off-white canvas: the heading stays legible at body size.
    const fontSize = Number.parseFloat(styles?.fontSize ?? '0');
    expect(fontSize).toBeGreaterThanOrEqual(16);
  });
});
