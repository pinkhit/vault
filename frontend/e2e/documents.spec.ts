import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';

async function signIn(
  page: Page,
  username = 'browser-admin',
  password = 'fictional-browser-password',
) {
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  await page.getByLabel('Username').fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Documents', exact: true }),
  ).toBeVisible();
}

test('upload, download exact bytes and delete with confirmation on desktop and mobile', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  await expect(page.getByText('Your library starts here')).toBeVisible();
  const fixture = '../tests/fixtures/fictional-invoice.txt';
  const original = await readFile(fixture);
  await page.getByLabel('Choose a document').setInputFiles(fixture);
  await page.getByRole('button', { name: 'Upload document' }).click();
  await expect(page.getByText('fictional-invoice.txt uploaded.')).toBeVisible();
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
  const verified = await page.request.get((await link.getAttribute('href'))!);
  expect(verified.headers()['x-checksum-sha256']).toBe(
    createHash('sha256').update(original).digest('hex'),
  );
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
  page.once('dialog', (dialog) => dialog.dismiss());
  await page
    .getByRole('button', { name: 'Delete fictional-invoice.txt' })
    .click();
  await expect(link).toBeVisible();
  page.once('dialog', (dialog) => dialog.accept());
  await page
    .getByRole('button', { name: 'Delete fictional-invoice.txt' })
    .click();
  await expect(page.getByText('Your library starts here')).toBeVisible();
  await page.reload();
  await expect(page.getByText('Your library starts here')).toBeVisible();
  await page.getByLabel('Account options').click();
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
    .getByLabel('Choose a document')
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

test('previews supported documents safely with keyboard/mobile support', async ({
  page,
}) => {
  await signIn(page);
  const errors: string[] = [];
  const dialogs: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('dialog', async (dialog) => {
    dialogs.push(dialog.message());
    await dialog.dismiss();
  });
  const session = await (await page.request.get('/api/auth/session')).json();
  const fixtures = [
    {
      name: '<img src=x onerror=alert(2)>.txt',
      mimeType: 'text/plain',
      buffer: Buffer.from('Preview sentinel <script>alert(1)</script>'),
      text: 'Preview sentinel <script>alert(1)</script>',
    },
    {
      name: 'preview.json',
      mimeType: 'application/json',
      buffer: Buffer.from('{"html":"<svg onload=alert(1)>","value":42}'),
      text: '"value": 42',
    },
    {
      name: 'preview <img>.docx',
      mimeType:
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      buffer: await readFile('../tests/fixtures/fictional-preview.docx'),
      text: '<script>literal DOCX text</script>',
    },
    {
      name: 'preview.csv',
      mimeType: 'text/csv',
      buffer: Buffer.from('Name,Value\n"Fictional, Ltd",=SUM(1;2)\n'),
      text: 'Fictional, Ltd',
    },
    {
      name: 'preview <img>.xlsx',
      mimeType:
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      buffer: await readFile('../tests/fixtures/fictional-preview.xlsx'),
      text: '<script>literal XLSX text</script>',
    },
    {
      name: 'preview.png',
      mimeType: 'image/png',
      buffer: await readFile('../tests/fixtures/fictional-preview.png'),
    },
    {
      name: 'preview.webp',
      mimeType: 'image/webp',
      buffer: await readFile('../tests/fixtures/fictional-preview.webp'),
    },
    {
      name: 'preview.pdf',
      mimeType: 'application/pdf',
      buffer: await readFile('../tests/fixtures/fictional-preview.pdf'),
    },
    {
      name: 'preview.svg',
      mimeType: 'image/svg+xml',
      buffer: Buffer.from('<svg onload="alert(1)"/>'),
      text: 'preview is unavailable for this file format',
    },
    {
      name: 'large-preview.txt',
      mimeType: 'text/plain',
      buffer: Buffer.from('x'.repeat(70_000)),
      text: 'Preview shortened',
    },
  ];
  const ids = new Map<string, string>();
  for (const fixture of fixtures) {
    const response = await page.request.post('/api/documents', {
      headers: { 'X-CSRF-Token': session.csrf_token },
      multipart: {
        file: {
          name: fixture.name,
          mimeType: fixture.mimeType,
          buffer: fixture.buffer,
        },
      },
    });
    expect(response.status()).toBe(201);
    ids.set(fixture.name, (await response.json()).id);
  }
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  for (const fixture of fixtures) {
    const opener = page.getByRole('button', {
      name: `Preview ${fixture.name}`,
      exact: true,
    });
    await opener.click();
    const viewer = page.getByRole('dialog');
    await expect(viewer).toBeVisible();
    await expect(
      viewer.getByRole('button', { name: 'Close preview' }),
    ).toBeFocused();
    await expect(viewer.getByText('Preparing preview…')).toHaveCount(0);
    await expect(
      viewer.locator('script, svg, iframe, object, embed'),
    ).toHaveCount(0);
    if (fixture.text) await expect(viewer).toContainText(fixture.text);
    else {
      const image = viewer.getByRole('img');
      await expect(image).toBeVisible();
      await expect
        .poll(() =>
          image.evaluate((node: HTMLImageElement) => node.naturalWidth),
        )
        .toBeGreaterThan(0);
      await expect(image).toHaveAttribute('src', /^data:image\/png;base64,/);
    }
    if (fixture.name.endsWith('.xlsx')) {
      await expect(viewer.getByRole('caption')).toContainText('Invoices <img>');
      await expect(
        viewer.getByRole('cell', { name: '=SUM(1,2)' }),
      ).toBeVisible();
      await expect(viewer.getByRole('status')).toHaveText('Sheet 1 of 2');
      await viewer.getByRole('button', { name: 'Next sheet' }).click();
      await expect(
        viewer.getByRole('cell', { name: 'Fictional XLSX summary 合同' }),
      ).toBeVisible();
      await expect(viewer.getByRole('status')).toHaveText('Sheet 2 of 2');
      await expect(
        viewer.getByRole('button', { name: 'Next sheet' }),
      ).toBeDisabled();
      await page.setViewportSize({ width: 390, height: 844 });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: 'test-results/xlsx-mobile.png',
        fullPage: true,
      });
      await viewer.getByRole('button', { name: 'Previous sheet' }).click();
      await expect(
        viewer.getByRole('cell', { name: '=SUM(1,2)' }),
      ).toBeVisible();
      await page.setViewportSize({ width: 1280, height: 720 });
    }
    if (fixture.name === 'preview.pdf') {
      await expect(viewer.getByRole('status')).toHaveText('Page 1 of 2');
      await viewer.getByRole('button', { name: 'Next page' }).click();
      await expect(viewer.getByRole('status')).toHaveText('Page 2 of 2');
      await expect(
        viewer.getByRole('button', { name: 'Next page' }),
      ).toBeDisabled();
      await page.screenshot({
        path: 'test-results/preview-desktop.png',
        fullPage: true,
      });
      await page.setViewportSize({ width: 390, height: 844 });
      await page.screenshot({
        path: 'test-results/preview-mobile.png',
        fullPage: true,
      });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
      await page.setViewportSize({ width: 1280, height: 720 });
    }
    await viewer.getByRole('button', { name: 'Close preview' }).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(
      viewer.getByRole('button', { name: 'Close preview' }),
    ).not.toBeFocused();
    expect(
      await viewer.evaluate((node) =>
        node.contains(window.document.activeElement),
      ),
    ).toBe(true);
    const download = page.waitForEvent('download');
    await viewer.getByRole('link', { name: 'Download original' }).click();
    expect(
      (await readFile((await (await download).path())!)).equals(fixture.buffer),
    ).toBe(true);
    await page.keyboard.press('Escape');
    await expect(viewer).toHaveCount(0);
    await expect(opener).toBeFocused();
  }
  // Search references open the same authorized viewer directly on the PDF page.
  const search = page.getByRole('searchbox');
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(
              `/api/documents/${ids.get('preview <img>.docx')}/indexing`,
            )
          ).json()
        ).status,
    )
    .toBe('ready');
  await search.fill('DOCX-BROWSER-2026');
  await search.press('Enter');
  await page
    .getByRole('button', { name: 'Preview preview <img>.docx', exact: true })
    .click();
  await expect(page.getByRole('dialog')).toContainText(
    'Fictional DOCX-BROWSER-2026 合同',
  );
  await expect(
    page.getByRole('dialog').locator('script, iframe, img'),
  ).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(
              `/api/documents/${ids.get('preview.pdf')}/indexing`,
            )
          ).json()
        ).status,
    )
    .toBe('ready');
  await search.fill('"preview page two"');
  await search.press('Enter');
  await page
    .getByRole('button', { name: 'Preview preview.pdf', exact: true })
    .click();
  await expect(page.getByRole('dialog').getByRole('status')).toHaveText(
    'Page 2 of 2',
  );
  expect(
    (
      await page.request.post('/api/auth/revoke-sessions', {
        headers: { 'X-CSRF-Token': session.csrf_token },
      })
    ).status(),
  ).toBe(204);
  await page.getByRole('button', { name: 'Refresh preview' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(dialogs).toEqual([]);
  expect(errors).toEqual([]);
});

test('shows administrator storage totals, survives metric errors and hides analytics from document managers', async ({
  page,
  browser,
  baseURL,
}) => {
  await signIn(page);
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const loaded = page.waitForResponse((response) =>
    response.url().endsWith('/api/admin/storage'),
  );
  await page
    .getByRole('button', { name: 'Storage overview', exact: true })
    .click();
  const initial = await (await loaded).json();
  const overview = page.getByRole('region', { name: 'Storage overview' });
  const count = overview
    .getByText('File count', { exact: true })
    .locator('..')
    .locator('dd');
  await expect(count).toHaveText(initial.documents.count.toLocaleString());
  if (initial.host_storage) {
    await expect(
      overview.getByRole('img', { name: /Mac storage:/ }),
    ).toBeVisible();
    await expect(
      overview.getByText('SafeVault (estimated)', { exact: true }),
    ).toBeVisible();
    await expect(
      overview.getByText('Other used (estimated)', { exact: true }),
    ).toBeVisible();
    const widths = await overview
      .locator('.storage-bar rect')
      .evaluateAll((rects) =>
        rects.map((rect) => Number(rect.getAttribute('width'))),
      );
    expect(widths).toHaveLength(4);
    expect(widths.reduce((sum, width) => sum + width, 0)).toBeCloseTo(1000);
  } else {
    await expect(overview).toContainText('Host storage is unavailable');
  }
  await overview.getByText('Docker volume limits').click();
  await expect(
    overview.getByText('Stored file size', { exact: true }),
  ).toBeVisible();
  if (initial.filesystem.source === 'container-filesystem') {
    await expect(overview).toContainText(
      'do not measure the Mac’s physical disk',
    );
  }
  await page.getByRole('button', { name: 'Documents', exact: true }).click();
  const payload = Buffer.from('Fictional storage count fixture');
  await page.getByLabel('Choose a document').setInputFiles({
    name: 'storage-counter.txt',
    mimeType: 'text/plain',
    buffer: payload,
  });
  const uploaded = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/documents'),
  );
  await page.getByRole('button', { name: 'Upload document' }).click();
  const document = await (await uploaded).json();
  const refreshed = page.waitForResponse((response) =>
    response.url().endsWith('/api/admin/storage'),
  );
  await page.getByRole('button', { name: 'Storage overview' }).click();
  const current = await (await refreshed).json();
  await overview.getByRole('button', { name: 'Refresh storage' }).click();
  expect(current.documents.count).toBe(initial.documents.count + 1);
  expect(current.documents.logical_bytes).toBe(
    initial.documents.logical_bytes + payload.length,
  );
  await expect(count).toHaveText(current.documents.count.toLocaleString());
  await page.screenshot({
    path: 'test-results/storage-desktop.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: 'test-results/storage-mobile.png',
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });

  // Synthetic provider failure: no capacity numbers, but logical totals remain.
  await page.route('**/api/admin/storage', (route) =>
    route.fulfill({
      json: {
        ...current,
        filesystem: { ...current.filesystem, capacity: null },
      },
    }),
  );
  await overview.getByRole('button', { name: 'Refresh storage' }).click();
  await expect(overview).toContainText('Filesystem capacity is unavailable');
  await expect(count).toHaveText(current.documents.count.toLocaleString());
  await expect(
    overview.getByText('Volume capacity', { exact: true }),
  ).toHaveCount(0);
  await page.unroute('**/api/admin/storage');
  await page.route('**/api/admin/storage', (route) =>
    route.fulfill({
      status: 503,
      json: { error: 'Synthetic metrics failure' },
    }),
  );
  await overview.getByRole('button', { name: 'Refresh storage' }).click();
  await expect(overview.getByRole('alert')).toHaveText(
    'Synthetic metrics failure',
  );
  await expect(count).toHaveCount(0);
  await page.unroute('**/api/admin/storage');
  await overview.getByRole('button', { name: 'Refresh storage' }).click();
  await expect(count).toBeVisible();

  const session = await (await page.request.get('/api/auth/session')).json();
  const headers = { 'X-CSRF-Token': session.csrf_token };
  const created = await page.request.post('/api/admin/users', {
    headers,
    data: {
      username: 'browser-storage-member',
      password: 'fictional-storage-member-password',
      is_admin: false,
    },
  });
  expect(created.status()).toBe(201);
  const member = await created.json();
  expect(
    (
      await page.request.put(
        `/api/documents/${document.id}/permissions/${member.id}`,
        { headers, data: { level: 'manage' } },
      )
    ).status(),
  ).toBe(204);
  const memberContext = await browser.newContext({ baseURL });
  try {
    const memberPage = await memberContext.newPage();
    await signIn(
      memberPage,
      'browser-storage-member',
      'fictional-storage-member-password',
    );
    await expect(
      memberPage.getByRole('link', { name: 'Download storage-counter.txt' }),
    ).toBeVisible();
    await expect(
      memberPage.getByRole('button', { name: 'Storage overview' }),
    ).toHaveCount(0);
    const denied = await memberPage.request.get('/api/admin/storage');
    expect(denied.status()).toBe(403);
    expect(await denied.json()).toEqual({ error: 'permission denied' });
  } finally {
    await memberContext.close();
  }
  expect(
    (
      await page.request.post('/api/auth/revoke-sessions', { headers })
    ).status(),
  ).toBe(204);
  await overview.getByRole('button', { name: 'Refresh storage' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Storage overview' }),
  ).toHaveCount(0);
  expect(errors).toEqual([]);
});
