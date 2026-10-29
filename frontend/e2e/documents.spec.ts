import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';

async function signIn(page: Page) {
  await page.goto('/');
  await page.getByLabel('Username').fill('browser-admin');
  await page
    .getByLabel('Password', { exact: true })
    .fill('fictional-browser-password');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Documents', exact: true }),
  ).toBeVisible();
}

test('upload, reload and download exact original bytes on desktop and mobile', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  await page.getByLabel('Username').fill('browser-admin');
  await page
    .getByLabel('Password', { exact: true })
    .fill('fictional-browser-password');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Documents', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('Your library starts here')).toBeVisible();
  const fixture = '../tests/fixtures/fictional-invoice.txt';
  const original = await readFile(fixture);
  await page.getByLabel('Choose a fictional document').setInputFiles(fixture);
  await page.getByRole('button', { name: 'Upload document' }).click();
  await expect(
    page.getByText('fictional-invoice.txt uploaded. SHA-256 verified.'),
  ).toBeVisible();
  await page.reload();
  const link = page.getByRole('link', {
    name: 'Download fictional-invoice.txt',
  });
  await expect(link).toBeVisible();
  const downloadPromise = page.waitForEvent('download');
  await link.click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('fictional-invoice.txt');
  const saved = await download.path();
  const bytes = await readFile(saved!);
  expect(bytes.equals(original)).toBe(true);
  await page.getByText('SHA-256 checksum').click();
  await expect(
    page.getByText(createHash('sha256').update(original).digest('hex')),
  ).toBeVisible();
  await page.screenshot({
    path: 'test-results/documents-desktop.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole('heading', { name: 'Documents', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Upload document' }),
  ).toBeVisible();
  await page.screenshot({
    path: 'test-results/documents-mobile.png',
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

test('searches indexed text safely on desktop/mobile and clears results after revocation', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  const name = '<img src=x onerror=alert(1)>.txt';
  const original = Buffer.from(
    'Fictional UISEARCH-2026-001 invoice. <svg onload=alert(1)>Literal text</svg>',
  );
  await page
    .getByLabel('Choose a fictional document')
    .setInputFiles({ name, mimeType: 'text/plain', buffer: original });
  const uploaded = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/documents'),
  );
  await page.getByRole('button', { name: 'Upload document' }).click();
  const uploadedDocument = await (await uploaded).json();
  await expect
    .poll(async () => {
      const response = await page.request.get(
        `/api/documents/${uploadedDocument.id}/indexing`,
      );
      return (await response.json()).status;
    })
    .toBe('ready');
  const search = page.getByRole('searchbox', { name: 'Search documents' });
  await search.fill('UISEARCH-2026-001');
  await search.press('Enter');
  const source = page.getByRole('link', { name: `Download ${name}` });
  await expect(source).toHaveAttribute(
    'href',
    `/api/documents/${uploadedDocument.id}/download`,
  );
  await expect(
    page.getByText('Text searchable', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/Text match · Section 1/)).toBeVisible();
  await expect(page.locator('.search-snippet')).toContainText('<svg');
  await expect(
    page.locator(
      '.search-results script, .search-results img, .search-results svg',
    ),
  ).toHaveCount(0);
  const downloadPromise = page.waitForEvent('download');
  await source.click();
  const downloaded = await downloadPromise;
  expect((await readFile((await downloaded.path())!)).equals(original)).toBe(
    true,
  );
  await page.screenshot({
    path: 'test-results/search-desktop.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(search).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: 'test-results/search-mobile.png',
    fullPage: true,
  });
  await search.fill('nomatches-ui-identifier');
  await search.press('Enter');
  await expect(page.getByText('No matching documents')).toBeVisible();
  await expect(source).toHaveCount(0);
  await search.fill('NEAR(printer)');
  await search.press('Enter');
  await expect(page.getByRole('alert')).toContainText('search requires');
  await search.fill('UISEARCH-2026-001');
  await search.press('Enter');
  await expect(source).toBeVisible();
  const session = await (await page.request.get('/api/auth/session')).json();
  const revoked = await page.request.post('/api/auth/revoke-sessions', {
    headers: { 'X-CSRF-Token': session.csrf_token },
  });
  expect(revoked.status()).toBe(204);
  await page.getByRole('button', { name: 'Refresh results' }).click();
  await expect(
    page.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Search results', exact: true }),
  ).toHaveCount(0);
  expect(errors).toEqual([]);
});
