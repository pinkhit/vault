import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

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
