import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { chromium } from 'playwright-core'
import { singerFavoriteSong, singerSubmitSong } from '../lib/micup.mjs'
import { findChromeExecutable } from '../lib/runner.mjs'

let browser
before(async () => {
  browser = await chromium.launch({ executablePath: findChromeExecutable(), headless: true })
})
after(async () => {
  await browser?.close()
})

test('favorites selects an unsaved track without removing an existing favorite', async (t) => {
  const page = await browser.newPage()
  t.after(() => page.close())
  await page.setContent(`
    <div><button title="Favorited track (tap to remove)" aria-label="Favorited: Saved">★</button>
      <div class="song-catalog-title">Saved</div></div>
    <div><button title="Save track to favorites" aria-label="Favorite New">☆</button>
      <div class="song-catalog-title">New</div></div>
    <script>
      document.querySelector('[title="Save track to favorites"]').onclick = (event) => {
        event.target.title = 'Favorited track (tap to remove)'
        event.target.setAttribute('aria-label', 'Favorited: New')
      }
    </script>
  `)
  const song = await singerFavoriteSong({ page })
  assert.equal(song.title, 'New')
  assert.equal(await page.getByRole('button', { name: 'Favorited: Saved' }).count(), 1)
  assert.equal(await page.getByRole('button', { name: 'Favorited: New' }).count(), 1)
})

test('song submission selects the requested pitch and sends it', async (t) => {
  const page = await browser.newPage()
  t.after(() => page.close())
  await page.route('http://vignette.test/**', async (route) => {
    if (route.request().method() === 'POST') {
      const body = JSON.parse(route.request().postData())
      await route.fulfill({ json: body })
      return
    }
    await route.fulfill({ contentType: 'text/html', body: `
      <input placeholder="anthem">
      <button class="btn-primary btn-sm" onclick="document.querySelector('[role=dialog]').hidden = false">Request</button>
      <div role="dialog" aria-labelledby="song-request-title" hidden>
        <h1 id="song-request-title">Song</h1>
        <input placeholder="Freddie">
        <button type="button" onclick="window.pitch = 1">+1 ♯</button>
        <form onsubmit="event.preventDefault(); fetch('/requests', {method:'POST', body:JSON.stringify({key_change:window.pitch || 0})}).then(() => document.querySelector('[role=dialog]').hidden = true)">
          <button type="submit">Submit</button>
        </form>
      </div>
    ` })
  })
  await page.goto('http://vignette.test/')
  const result = await singerSubmitSong({ page, authenticated: false }, { singerName: 'Alex', keyShift: '+1' })
  assert.equal(result.ok, true)
  assert.equal(result.payload.key_change, 1)
})
