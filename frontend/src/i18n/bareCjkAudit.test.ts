import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const SRC_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SOURCE_EXT_RE = /\.(vue|ts|tsx|js|mjs|cjs)$/
const TEST_FILE_RE = /\.test\.(ts|tsx|js|mjs|cjs)$/
const BARE_CJK_RE = /[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]/

/**
 * Explicit allowlist of the ONLY 25 remaining view/component files scheduled
 * for W7–W8 CJK localization (using pre-seeded keys in `src/i18n/locales/*.ts`).
 * All foundation layers (`utils/`, `composables/`, `hooks/`, `directives/`,
 * `stores/`, `api/`, `router/`, `types/`, `config/`, `assets/`, `i18n/`) MUST
 * maintain 0 bare CJK literals immediately.
 */
export const W7_W8_PENDING_CJK_ALLOWLIST: ReadonlySet<string> = new Set([
  'components/AgentEmbedChannelPanel.vue',
  'components/GlobalCommandPalette/commands.ts',
  'components/IMChannelPanel.vue',
  'components/ModelEditorDialog.vue',
  'components/SessionSidebarRow.vue',
  'components/UserMenu.vue',
  'components/document-preview.vue',
  'components/menu.vue',
  'views/auth/Login.vue',
  'views/chat/components/AgentStreamDisplay.vue',
  'views/chat/components/botmsg.vue',
  'views/chat/components/usermsg.vue',
  'views/chat/index.vue',
  'views/dev/MarkdownTestPage.vue',
  'views/knowledge/KnowledgeBase.vue',
  'views/knowledge/components/FAQEntryManager.vue',
  'views/knowledge/settings/chunkingSamples.ts',
  'views/knowledge/wiki/WikiBrowser.vue',
  'views/settings/ModelSettings.vue',
  'views/settings/OllamaSettings.vue',
  'views/settings/ParserEngineSettings.vue',
  'views/settings/StorageBackendSettings.vue',
  'views/settings/VectorStoreSettings.vue',
  'views/settings/WebSearchSettings.vue',
  'views/settings/components/McpServiceDialog.vue',
])

const STRICT_ZERO_CJK_DIRS = [
  'utils/',
  'composables/',
  'hooks/',
  'directives/',
  'stores/',
  'api/',
  'router/',
  'types/',
  'config/',
  'assets/',
  'i18n/',
] as const

/**
 * Blank out block, HTML, and line comments while preserving URLs (`://`),
 * escaped regex slashes (`\/\/`), and exact line numbers.
 */
export function stripSourceComments(source: string): string {
  const blank = (match: string) => match.replace(/[^\n]/g, ' ')
  return source
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .replace(/<!--[\s\S]*?-->/g, blank)
    .replace(/(?<!:|\\)\/\/[^\n]*/gm, blank)
}

function shouldScanFile(relPath: string): boolean {
  if (!SOURCE_EXT_RE.test(relPath)) return false
  if (TEST_FILE_RE.test(relPath)) return false
  if (relPath.startsWith('i18n/locales/') || relPath.startsWith('i18n/embed-locales/')) return false
  if (relPath.startsWith('wailsjs/') || relPath.startsWith('test-utils/')) return false
  return true
}

function walkSources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === 'dist') continue
    const fullPath = join(dir, entry.name)
    if (entry.isDirectory()) {
      walkSources(fullPath, out)
      continue
    }
    const relPath = relative(SRC_ROOT, fullPath).replaceAll('\\', '/')
    if (shouldScanFile(relPath)) out.push(relPath)
  }
  return out
}

type CjkHit = {
  relPath: string
  line: number
  snippet: string
}

function scanBareCjkHits(): CjkHit[] {
  const hits: CjkHit[] = []
  for (const relPath of walkSources(SRC_ROOT)) {
    const raw = readFileSync(join(SRC_ROOT, relPath), 'utf8')
    const strippedLines = stripSourceComments(raw).split('\n')
    const rawLines = raw.split('\n')
    strippedLines.forEach((lineText, idx) => {
      if (BARE_CJK_RE.test(lineText)) {
        hits.push({
          relPath,
          line: idx + 1,
          snippet: (rawLines[idx] ?? '').trim().slice(0, 120),
        })
      }
    })
  }
  return hits
}

test('foundation directories contain zero bare CJK literals', () => {
  const hits = scanBareCjkHits().filter((hit) =>
    STRICT_ZERO_CJK_DIRS.some((prefix) => hit.relPath.startsWith(prefix)),
  )
  assert.deepEqual(
    hits.map((h) => `${h.relPath}:${h.line}: ${h.snippet}`),
    [],
    `Unexpected bare CJK literals in foundation directories:\n${hits
      .slice(0, 25)
      .map((h) => `  ${h.relPath}:${h.line}  ${h.snippet}`)
      .join('\n')}`,
  )
})

test('no unallowlisted source file introduces bare CJK literals', () => {
  const disallowed = scanBareCjkHits().filter(
    (hit) => !W7_W8_PENDING_CJK_ALLOWLIST.has(hit.relPath),
  )
  assert.deepEqual(
    disallowed.map((h) => `${h.relPath}:${h.line}: ${h.snippet}`),
    [],
    `Bare CJK literals found outside W7/W8 allowlist:\n${disallowed
      .slice(0, 25)
      .map((h) => `  ${h.relPath}:${h.line}  ${h.snippet}`)
      .join('\n')}`,
  )
})
