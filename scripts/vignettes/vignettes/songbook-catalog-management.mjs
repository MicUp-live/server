import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import {
  createSongbookViaUI,
  ensureHostLoggedIn,
  hostDeleteSongbookViaApi,
} from '../lib/micup.mjs'

export const name = 'songbook-catalog-management'

export async function run(runner, config) {
  const host = await runner.addActor('host', {
    label: 'Host (KJ Desktop)',
    role: 'host',
    storageStatePath: config.hostStorageState,
  })
  const bookName = `Vignette Library ${Date.now()}`
  const updatedName = `${bookName} Updated`
  let songbook
  const { page } = host

  try {
    await ensureHostLoggedIn(host, { email: config.hostEmail, password: config.hostPassword })
    songbook = await createSongbookViaUI(host, bookName)
    assert.ok(songbook.id, 'Created songbook has an id')
    await runner.snap(
      'songbook-created',
      'Host creates a songbook',
      'The new book appears in the host library before any catalog tracks are added.',
      ['host']
    )

    let card = page.locator('.kj-songbook-card').filter({ hasText: bookName })
    await card.getByRole('button', { name: `More actions for ${bookName}` }).click()
    await page.getByRole('button', { name: 'Edit Details' }).click()
    const editInputs = page.locator('.kj-songbook-card input')
    await editInputs.nth(0).fill(updatedName)
    await editInputs.nth(1).fill(`vignette-${Date.now()}`)
    await page.getByRole('button', { name: 'Save Changes' }).click()
    card = page.locator('.kj-songbook-card').filter({ hasText: updatedName })
    await card.getByText(/^CODE:/).waitFor()
    await runner.snap(
      'songbook-details-updated',
      'Host renames the book and assigns a public code',
      'The new name and custom slug are shown on the host songbook card.',
      ['host']
    )

    await card.getByRole('button', { name: `More actions for ${updatedName}` }).click()
    await page.getByRole('button', { name: 'Publish Songbook' }).click()
    await card.getByText('● Public').waitFor()

    await card.getByRole('button', { name: 'Manage Songs' }).click()
    await page.getByText('No songs in this songbook yet.').waitFor()
    await page.getByPlaceholder('Artist * (e.g. Queen)').fill('Vignette Artist')
    await page.getByPlaceholder('Title * (e.g. Bohemian Rhapsody)').fill('First Library Track')
    await page.getByPlaceholder('Disc / Code (e.g. SC8100-01)').fill('VIG-001')
    await page.getByRole('button', { name: /\+ Add Song/ }).click()
    let firstTrack = page.locator('.kj-sm-song-card').filter({ hasText: 'First Library Track' })
    await firstTrack.waitFor()

    await page.getByPlaceholder('Artist * (e.g. Queen)').fill('Second Vignette Artist')
    await page.getByPlaceholder('Title * (e.g. Bohemian Rhapsody)').fill('Second Library Track')
    await page.getByRole('button', { name: /\+ Add Song/ }).click()
    const secondTrack = page.locator('.kj-sm-song-card').filter({ hasText: 'Second Library Track' })
    await secondTrack.waitFor()

    await firstTrack.getByRole('button', { name: 'Edit' }).click()
    const editDialog = page.getByRole('dialog', { name: 'Edit Song' })
    await editDialog.getByLabel('Title *').fill('Edited Library Track')
    await editDialog.getByLabel('Disc / Code').fill('VIG-001-EDIT')
    await editDialog.getByRole('button', { name: 'Save Changes' }).click()
    firstTrack = page.locator('.kj-sm-song-card').filter({ hasText: 'Edited Library Track' })
    await firstTrack.waitFor()

    page.once('dialog', (dialog) => dialog.accept())
    await secondTrack.getByRole('button', { name: 'Delete Second Library Track' }).click()
    await secondTrack.waitFor({ state: 'detached' })

    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: /Export CSV/ }).click(),
    ])
    assert.match(download.suggestedFilename(), /_catalog\.csv$/)
    const csvPath = await download.path()
    assert.ok(csvPath, 'CSV export download completed')
    const csv = await fs.readFile(csvPath, 'utf8')
    assert.match(csv, /Edited Library Track/)
    assert.doesNotMatch(csv, /Second Library Track/)
    await runner.snap(
      'songbook-catalog-managed',
      'Host adds, edits, removes, and exports catalog tracks',
      'The manager and CSV export contain the edited track and omit the deleted track.',
      ['host']
    )

    await page.getByRole('button', { name: '← Back' }).click()
    card = page.locator('.kj-songbook-card').filter({ hasText: updatedName })
    await card.waitFor()
    await card.getByRole('button', { name: `More actions for ${updatedName}` }).click()
    await page.getByRole('button', { name: 'Make Private' }).click()
    await card.getByText('○ Private').waitFor()
    await runner.snap(
      'songbook-privacy-managed',
      'Host makes the songbook private again',
      'The library reflects the updated publication visibility after catalog edits.',
      ['host']
    )

    await card.getByRole('button', { name: 'Manage Access' }).click()
    const accessDialog = page.getByRole('dialog', { name: updatedName })
    const inviteEmail = `vignette-${Date.now()}@example.test`
    await accessDialog.getByLabel('Recipient email address').fill(inviteEmail)
    await accessDialog.getByRole('checkbox', { name: /Edit catalog/ }).check()
    await accessDialog.getByRole('button', { name: 'Send invitation' }).click()
    await accessDialog.getByText(inviteEmail).waitFor()
    await accessDialog.getByText(/Edit catalog · Host shows/).waitFor()
    await runner.snap(
      'songbook-collaborator-invited',
      'Owner reviews a pending collaborator invitation',
      'The pending invite displays its recipient and catalog-edit/hosting permissions.',
      ['host']
    )
    await accessDialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    await accessDialog.getByText('No pending invitations.').waitFor()
  } finally {
    if (songbook?.id) await hostDeleteSongbookViaApi(host, songbook.id)
  }
}
