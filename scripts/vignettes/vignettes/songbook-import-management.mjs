import assert from 'node:assert/strict'
import {
  createSongbookViaUI,
  ensureHostLoggedIn,
  hostDeleteSongbookViaApi,
} from '../lib/micup.mjs'

export const name = 'songbook-import-management'

async function uploadCsv(page, card, filename, content, expectedRows) {
  await card.getByRole('button', { name: 'Assistant Sync' }).first().click()
  await page.getByRole('button', { name: 'File Upload' }).click()
  await page.locator('input[type="file"][accept*=".csv"]').setInputFiles({
    name: filename,
    mimeType: 'text/csv',
    buffer: Buffer.from(content),
  })
  const preview = page.getByRole('region', { name: 'Parsed songs preview' })
  await preview.locator('tbody tr').nth(expectedRows - 1).waitFor()
  assert.equal(await preview.locator('tbody tr').count(), expectedRows)
  return preview
}

export async function run(runner, config) {
  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    storageStatePath: config.hostStorageState,
  })
  const bookName = `Vignette Import ${Date.now()}`
  let songbook
  const { page } = host

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    songbook = await createSongbookViaUI(host, bookName)
    let card = page.locator('.kj-songbook-card').filter({ hasText: bookName })

    const basePreview = await uploadCsv(
      page,
      card,
      'vignette-base-catalog.csv',
      [
        'Artist,Title,Disc Code',
        'Vignette Import Artist,Base Track One,VIG-BASE-001',
        'Vignette Import Artist,Base Track Two,VIG-BASE-002',
        'Vignette Import Artist,Base Track Three,VIG-BASE-003',
      ].join('\n'),
      3
    )
    await basePreview.getByText('Base Track One').waitFor()
    await runner.snap(
      'import-preview-reviewed',
      'Host reviews a parsed catalog file',
      'Three locally generated CSV rows are parsed and shown in the import audit before submission.',
      ['host']
    )
    await page.getByRole('button', { name: 'Import 3 Songs' }).click()
    await card.getByText('3 songs').waitFor({ timeout: 60000 })

    const additivePreview = await uploadCsv(
      page,
      card,
      'vignette-additive-update.csv',
      [
        'Artist,Title,Disc Code',
        'Vignette Import Artist,Added by Second Import,VIG-ADD-001',
      ].join('\n'),
      1
    )
    await additivePreview.getByText('Added by Second Import').waitFor()
    const pruneMissing = page.getByRole('checkbox', { name: /Hide songs missing from this import/ })
    assert.equal(await pruneMissing.isChecked(), false, 'Additive sync is the default')
    await runner.snap(
      'additive-import-selected',
      'Host leaves replacement pruning disabled',
      'The default import intent is additive, so omitted tracks are retained.',
      ['host']
    )
    await page.getByRole('button', { name: 'Import 1 Songs' }).click()
    card = page.locator('.kj-songbook-card').filter({ hasText: bookName })
    await card.getByText('4 songs').waitFor({ timeout: 60000 })

    await card.getByRole('button', { name: 'Manage Songs' }).click()
    for (const title of [
      'Base Track One',
      'Base Track Two',
      'Base Track Three',
      'Added by Second Import',
    ]) {
      await page.locator('.kj-sm-song-card').filter({ hasText: title }).waitFor()
    }
    assert.equal(await page.locator('.kj-sm-song-card').count(), 4)
    await runner.snap(
      'additive-import-preserves-catalog',
      'Additive import keeps prior songs and adds the new track',
      'All three original entries remain alongside the track from the second import.',
      ['host']
    )

    await page.getByRole('button', { name: '← Back' }).click()
    card = page.locator('.kj-songbook-card').filter({ hasText: bookName })
    const replacementPreview = await uploadCsv(
      page,
      card,
      'vignette-replacement.csv',
      [
        'Artist,Title,Disc Code',
        'Vignette Replacement Artist,Replacement Track,VIG-REPLACE-001',
      ].join('\n'),
      1
    )
    await replacementPreview.getByText('Replacement Track').waitFor()
    await page.getByRole('checkbox', { name: /Hide songs missing from this import/ }).check()
    let uploadsStarted = 0
    const countUploads = (request) => {
      if (request.method() === 'POST' && request.url().endsWith(`/songbooks/${songbook.id}/import-uploads`)) {
        uploadsStarted++
      }
    }
    page.on('request', countUploads)
    const cancelDialog = page.waitForEvent('dialog')
    const cancelClick = page.getByRole('button', { name: 'Import 1 Songs' }).click()
    const warning = await cancelDialog
    assert.match(warning.message(), /importing 1 songs.*catalog currently has 4 songs/i)
    await warning.dismiss()
    await cancelClick
    assert.equal(uploadsStarted, 0, 'Canceling the warning does not dispatch an import')
    await replacementPreview.getByText('Replacement Track').waitFor()
    await runner.snap(
      'replacement-import-cancelled',
      'Host reviews and cancels the replacement warning',
      'A smaller replacement requires confirmation; cancel keeps the import preview open.',
      ['host']
    )

    const confirmDialog = page.waitForEvent('dialog')
    const confirmClick = page.getByRole('button', { name: 'Import 1 Songs' }).click()
    const confirmation = await confirmDialog
    assert.match(confirmation.message(), /importing 1 songs.*catalog currently has 4 songs/i)
    await confirmation.accept()
    await confirmClick
    page.off('request', countUploads)
    await card.getByText('1 song', { exact: true }).waitFor({ timeout: 60000 })
    await card.getByRole('button', { name: 'Manage Songs' }).click()
    await page.locator('.kj-sm-song-card').filter({ hasText: 'Replacement Track' }).waitFor()
    assert.equal(await page.locator('.kj-sm-song-card').count(), 1)
    assert.equal(await page.locator('.kj-sm-song-card').filter({ hasText: 'Base Track One' }).count(), 0)
    await runner.snap(
      'replacement-import-completed',
      'Confirmed replacement publishes only the new catalog',
      'The managed catalog contains the replacement track and no prior tracks.',
      ['host']
    )
  } finally {
    if (songbook?.id) await hostDeleteSongbookViaApi(host, songbook.id)
  }
}
