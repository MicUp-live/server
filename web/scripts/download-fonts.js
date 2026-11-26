import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const publicFontsDir = path.resolve(__dirname, '../public/fonts')
const stylesDir = path.resolve(__dirname, '../src/styles')

fs.mkdirSync(publicFontsDir, { recursive: true })

const googleFontsUrl = "https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@500;600&family=Plus+Jakarta+Sans:wght@400;500;600;700&family=Syne:wght@700;800&display=swap"

async function run() {
    console.log('Fetching Google Fonts CSS...')
    const res = await fetch(googleFontsUrl, {
        headers: {
            'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
        }
    })
    const css = await res.text()

    // Parse @font-face blocks
    const fontFaceBlocks = css.split('@font-face').slice(1)
    let localCss = `/* =========================================================================
   MicUp.live Self-Hosted Brand Fonts
   Syne, Plus Jakarta Sans, JetBrains Mono
   ========================================================================= */\n\n`

    let fontCounter = 0
    for (const block of fontFaceBlocks) {
        const familyMatch = /font-family:\s*['"]?([^'";]+)['"]?/.exec(block)
        const weightMatch = /font-weight:\s*([^;]+);/.exec(block)
        const styleMatch = /font-style:\s*([^;]+);/.exec(block)
        const displayMatch = /font-display:\s*([^;]+);/.exec(block)
        const urlMatch = /url\((https:\/\/[^)]+\.woff2)\)/.exec(block)
        const unicodeMatch = /unicode-range:\s*([^;]+);/.exec(block)

        if (!familyMatch || !weightMatch || !urlMatch) continue

        const family = familyMatch[1].trim()
        const weight = weightMatch[1].trim()
        const style = styleMatch ? styleMatch[1].trim() : 'normal'
        const fontUrl = urlMatch[1]
        const unicodeRange = unicodeMatch ? unicodeMatch[1].trim() : null

        const cleanFamilyName = family.toLowerCase().replace(/[^a-z0-9]/g, '-')
        const filename = `${cleanFamilyName}-${weight}-${fontCounter++}.woff2`
        const localPath = path.join(publicFontsDir, filename)

        console.log(`Downloading ${family} (weight: ${weight}) -> ${filename}...`)
        const fontRes = await fetch(fontUrl)
        const fontBuffer = Buffer.from(await fontRes.arrayBuffer())
        fs.writeFileSync(localPath, fontBuffer)

        localCss += `@font-face {\n`
        localCss += `  font-family: '${family}';\n`
        localCss += `  font-style: ${style};\n`
        localCss += `  font-weight: ${weight};\n`
        localCss += `  font-display: swap;\n`
        localCss += `  src: url('/fonts/${filename}') format('woff2');\n`
        if (unicodeRange) {
            localCss += `  unicode-range: ${unicodeRange};\n`
        }
        localCss += `}\n\n`
    }

    const fontsCssPath = path.join(stylesDir, 'fonts.css')
    fs.writeFileSync(fontsCssPath, localCss, 'utf8')
    console.log(`✅ Saved all self-hosted fonts to public/fonts/ and created ${fontsCssPath}`)
}

run().catch(err => {
    console.error('Failed to download fonts:', err)
    process.exit(1)
})
