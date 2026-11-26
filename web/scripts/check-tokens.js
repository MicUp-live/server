import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const srcDir = path.resolve(__dirname, '../src')
const tokensFile = path.resolve(srcDir, 'styles/tokens.css')

if (!fs.existsSync(tokensFile)) {
    console.error(`Error: Tokens file not found at ${tokensFile}`)
    process.exit(1)
}

// 1. Extract defined tokens from tokens.css
const tokensContent = fs.readFileSync(tokensFile, 'utf8')
const definedTokens = new Set()
const tokenDefRegex = /(--[a-zA-Z0-9_-]+)\s*:/g
let match
while ((match = tokenDefRegex.exec(tokensContent)) !== null) {
    definedTokens.add(match[1])
}

console.log(`Found ${definedTokens.size} declared tokens in tokens.css`)

// 2. Scan src/ for all var(--...) references
const fileExtensions = new Set(['.jsx', '.js', '.css', '.html'])
const undeclaredUsages = []

function scanDirectory(dir) {
    const entries = fs.readdirSync(dir, { withFileTypes: true })
    for (const entry of entries) {
        const fullPath = path.join(dir, entry.name)
        if (entry.isDirectory()) {
            scanDirectory(fullPath)
        } else if (fileExtensions.has(path.extname(entry.name))) {
            checkFile(fullPath)
        }
    }
}

function checkFile(filePath) {
    const content = fs.readFileSync(filePath, 'utf8')
    const lines = content.split('\n')
    
    // Also capture any file-level or component-level defined CSS variables (e.g. in CSS files or style blocks)
    const localDefined = new Set(definedTokens)
    const localDefRegex = /(--[a-zA-Z0-9_-]+)\s*:/g
    let localMatch
    while ((localMatch = localDefRegex.exec(content)) !== null) {
        localDefined.add(localMatch[1])
    }

    const varRegex = /var\(\s*(--[a-zA-Z0-9_-]+)/g
    lines.forEach((line, idx) => {
        let varMatch
        while ((varMatch = varRegex.exec(line)) !== null) {
            const tokenName = varMatch[1]
            if (!localDefined.has(tokenName)) {
                undeclaredUsages.push({
                    file: path.relative(path.resolve(__dirname, '..'), filePath),
                    line: idx + 1,
                    token: tokenName,
                    snippet: line.trim()
                })
            }
        }
    })
}

scanDirectory(srcDir)

if (undeclaredUsages.length > 0) {
    console.error(`\n❌ Found ${undeclaredUsages.length} undeclared CSS token reference(s):`)
    undeclaredUsages.forEach(u => {
        console.error(`  ${u.file}:${u.line} - Unknown token: "${u.token}"\n    ${u.snippet}`)
    })
    console.error('\nPlease define all tokens in src/styles/tokens.css before referencing them.')
    process.exit(1)
}

console.log('✅ Token Contract Enforced: All referenced CSS variables are declared in tokens.css.')
