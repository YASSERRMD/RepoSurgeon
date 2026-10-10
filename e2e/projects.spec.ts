import { expect, test } from '@playwright/test';

/**
 * M01 browser journey against the composed stack: create an organization and
 * a project from the UI, reload, and confirm the data really persisted in
 * PostgreSQL. No mocks — every assertion reflects a stored record.
 */
test.describe('project workbench', () => {
  const unique = Date.now().toString(36);
  const orgName = `E2E Org ${unique}`;
  const orgSlug = `e2e-org-${unique}`;
  const projectName = `e2e-project-${unique}`;

  test('create organization, create project, reload and filter', async ({ page, request }) => {
    const readiness = await request.get('/health/ready');
    test.skip(readiness.status() !== 200, 'stack is not ready; skipping M01 journey');

    // 1. Create an organization; the app makes it active and opens projects.
    await page.goto('/organizations');
    await page.locator('#org-name').fill(orgName);
    await page.locator('#org-slug').fill(orgSlug);
    await page.getByTestId('create-org-submit').click();
    await expect(page).toHaveURL(/\/projects$/, { timeout: 15000 });

    // 2. The organization is persisted: it is listed from the API.
    await page.goto('/organizations');
    const orgRow = page.locator('[data-testid^="org-org_"]').first();
    await expect(orgRow).toBeVisible();
    await expect(orgRow).toContainText(orgName);

    // 3. Create a project; the app opens its detail view.
    await page.goto('/projects');
    await page.getByTestId('new-project-button').click();
    await page.locator('#project-name').fill(projectName);
    await page.locator('#project-description').fill('created by the E2E journey');
    await page.getByTestId('create-project-submit').click();

    // 4. Detail view reflects the stored project and its audit trail.
    await expect(page.getByRole('heading', { name: projectName })).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId('audit-list')).toContainText('project.created', { timeout: 15000 });

    // 5. Back on the list the project persists after a full reload.
    await page.goto('/projects');
    await expect(page.getByText(projectName).first()).toBeVisible({ timeout: 15000 });
    await page.reload();
    await expect(page.getByText(projectName).first()).toBeVisible({ timeout: 15000 });

    // 6. Filtering narrows the list and a non-matching filter hides it.
    await page.getByTestId('project-search').fill(projectName);
    await page.getByTestId('project-search-submit').click();
    await expect(page.getByText(projectName).first()).toBeVisible();
    await page.getByTestId('project-search').fill('no-such-project-name');
    await page.getByTestId('project-search-submit').click();
    await expect(page.getByText(projectName).first()).toBeHidden();

    // 7. Reopen the detail view from the filtered list.
    await page.getByTestId('project-search').fill(projectName);
    await page.getByTestId('project-search-submit').click();
    await page.getByText(projectName).first().click();
    await expect(page.getByRole('heading', { name: projectName })).toBeVisible();
  });
});
