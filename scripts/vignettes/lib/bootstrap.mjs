import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { chromium } from 'playwright-core'
import { findChromeExecutable } from './runner.mjs'

const SAMPLE_SONGS = [
  { artist: 'Journey', title: "Don't Stop Believin'" },
  { artist: 'Queen', title: 'Bohemian Rhapsody' },
  { artist: 'Whitney Houston', title: 'I Wanna Dance with Somebody' },
  { artist: 'The Killers', title: 'Mr. Brightside' },
  { artist: 'Bon Jovi', title: "Livin' on a Prayer" },
  { artist: 'Fleetwood Mac', title: 'Dreams' },
  { artist: 'Neil Diamond', title: 'Sweet Caroline' },
  { artist: 'ABBA', title: 'Dancing Queen' },
  { artist: 'Dolly Parton', title: 'Jolene' },
  { artist: 'Toto', title: 'Africa' },
  { artist: 'TLC', title: 'No Scrubs' },
  { artist: 'Backstreet Boys', title: 'I Want It That Way' },
]

function randomPassword() {
  return `MicUp!${crypto.randomBytes(12).toString('base64url')}`
}

async function seedSongbookInNode({ baseUrl, songs, token }) {
  const headers = {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  }
  const sbRes = await fetch(`${baseUrl}/api/v1/songbooks`, {
    method: 'POST',
    headers,
    body: JSON.stringify({
      name: 'Vignette Sample Book',
      description: 'Automated Playwright vignette test catalog',
    }),
  })
  if (!sbRes.ok) {
    throw new Error(`Create songbook failed (${sbRes.status}): ${await sbRes.text()}`)
  }
  const sb = await sbRes.json()
  const manifest = JSON.stringify({
    songs: songs.map((s) => ({
      artist: s.artist,
      title: s.title,
      is_custom: true,
      is_available: true,
    })),
    prune_missing: false,
  })
  const upRes = await fetch(`${baseUrl}/api/v1/songbooks/${encodeURIComponent(sb.id)}/import-uploads`, {
    method: 'POST',
    headers,
    body: JSON.stringify({
      total_songs: songs.length,
      size: Buffer.byteLength(manifest),
      prune_missing: false,
    }),
  })
  if (!upRes.ok) {
    throw new Error(`Create import upload failed (${upRes.status}): ${await upRes.text()}`)
  }
  const upload = await upRes.json()
  const putRes = await fetch(upload.upload_url, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: manifest,
  })
  if (!putRes.ok) {
    throw new Error(`S3 manifest PUT failed (${putRes.status})`)
  }
  const compRes = await fetch(
    `${baseUrl}/api/v1/songbooks/${encodeURIComponent(sb.id)}/import-jobs/${encodeURIComponent(upload.job.id)}/complete`,
    { method: 'POST', headers }
  )
  if (!compRes.ok) {
    throw new Error(`Complete import failed (${compRes.status}): ${await compRes.text()}`)
  }
  let job = await compRes.json()
  for (let i = 0; i < 40 && job?.status !== 'completed'; i++) {
    if (job?.status === 'failed') {
      throw new Error(`Import job failed: ${job.error || 'unknown'}`)
    }
    await new Promise((r) => setTimeout(r, 1500))
    const pollRes = await fetch(
      `${baseUrl}/api/v1/songbooks/${encodeURIComponent(sb.id)}/import-jobs/${encodeURIComponent(job.id)}`,
      { headers }
    )
    if (pollRes.ok) job = await pollRes.json()
  }
  if (job?.status !== 'completed') {
    throw new Error(`Import job timed out (last status: ${job?.status})`)
  }
  return { id: sb.id, name: sb.name, added: songs.length }
}

async function registerAndSave(browser, { baseUrl, email, password, displayName, isHost, statePath }) {
  const context = await browser.newContext({ baseURL: baseUrl })
  const page = await context.newPage()

  // Navigate to origin first so cookies & fetch are same-origin
  await page.goto('/login', { waitUntil: 'domcontentloaded' })

  const result = await page.evaluate(
    async ({ email, password, displayName, isHost }) => {
      const res = await fetch('/api/v1/auth/register', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          email,
          password,
          display_name: displayName,
          is_host: isHost,
        }),
      })
      const text = await res.text()
      return { ok: res.ok, status: res.status, text }
    },
    { email, password, displayName, isHost }
  )

  if (!result.ok) {
    await context.close()
    throw new Error(`Failed to register ${email} (${result.status}): ${result.text}`)
  }

  let songbook = null
  if (isHost) {
    const regData = JSON.parse(result.text)
    const token = regData.access_token || ''
    songbook = await seedSongbookInNode({ baseUrl, songs: SAMPLE_SONGS, token })
  }

  // Visit home so session/localStorage initializes cleanly before saving storageState
  await page.goto(isHost ? '/kj/queue' : '/', { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(800)
  await context.storageState({ path: statePath })
  await context.close()

  return { songbook }
}

export async function bootstrapBotAccounts({ baseUrl, authDir }) {
  fs.mkdirSync(authDir, { recursive: true })
  const tag = crypto.randomBytes(3).toString('hex')

  // Uses AWS SES zero-bounce simulator addresses so prod SES reputation is unaffected.
  const hostCreds = {
    email: `success+micup-vig-host-${tag}@simulator.amazonses.com`,
    password: randomPassword(),
    displayName: 'Vignette KJ Bot',
    isHost: true,
  }
  const singerCreds = {
    email: `success+micup-vig-singer-${tag}@simulator.amazonses.com`,
    password: randomPassword(),
    displayName: 'Alex Member',
    isHost: false,
  }

  const hostStatePath = path.join(authDir, 'host.json')
  const singerStatePath = path.join(authDir, 'singer-auth.json')
  const credsPath = path.join(authDir, 'credentials.json')

  const browser = await chromium.launch({
    executablePath: findChromeExecutable(),
    headless: true,
    args: ['--disable-dev-shm-usage'],
  })

  try {
    console.log(`Creating bot host account on ${baseUrl}: ${hostCreds.email}`)
    const hostRes = await registerAndSave(browser, {
      baseUrl,
      ...hostCreds,
      statePath: hostStatePath,
    })
    if (hostRes.songbook?.error) {
      throw new Error(hostRes.songbook.error)
    }
    console.log(
      `  Created songbook "${hostRes.songbook.name}" (${hostRes.songbook.id}) with ${hostRes.songbook.added} songs.`
    )

    console.log(`Creating bot singer account on ${baseUrl}: ${singerCreds.email}`)
    await registerAndSave(browser, {
      baseUrl,
      ...singerCreds,
      statePath: singerStatePath,
    })

    fs.writeFileSync(
      credsPath,
      JSON.stringify(
        {
          baseUrl,
          createdAt: new Date().toISOString(),
          host: { email: hostCreds.email, password: hostCreds.password, displayName: hostCreds.displayName },
          singerAuth: {
            email: singerCreds.email,
            password: singerCreds.password,
            displayName: singerCreds.displayName,
          },
          songbook: hostRes.songbook,
        },
        null,
        2
      ),
      { mode: 0o600 }
    )

    console.log(`Saved bot credentials & Playwright auth states under ${authDir}`)
    return { hostCreds, singerCreds, songbook: hostRes.songbook, credsPath }
  } finally {
    await browser.close()
  }
}
