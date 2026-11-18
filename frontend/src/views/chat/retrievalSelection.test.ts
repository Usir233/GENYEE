import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

const indexSource = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const scopeSource = readFileSync(new URL('./composables/useChatOutgoingAttachments.ts', import.meta.url), 'utf8')
const scopeFnStart = scopeSource.indexOf('export function resolveOutgoingResourceScope(')
assert.ok(scopeFnStart >= 0)
const scopeJs = ts.transpileModule(
  scopeSource.slice(scopeFnStart).replace('export function', 'function'),
  { compilerOptions: { target: ts.ScriptTarget.ES2022, alwaysStrict: false } },
).outputText

test('assistant page context does not replace or widen explicit KB, file and tag mentions', () => {
  assert.match(indexSource, /const hostScope = props\.embeddedMode && props\.hostScope \? props\.hostScope\(\) : null/)
  for (const mentionedItems of [
    [],
    [{ id: 'selected-kb', type: 'kb' }],
    [{ id: 'doc', type: 'file' }],
    [{ id: 'tag', type: 'tag', kb_id: 'selected-kb' }],
  ]) {
    const result = runInNewContext(
      `${scopeJs}
      const hostScope = props.embeddedMode && props.hostScope ? props.hostScope() : null;
      const { kbIds, knowledgeIds, tagIds } = resolveOutgoingResourceScope({
        embeddedMode: props.embeddedMode,
        hostScope,
        selectedKnowledgeBases: useSettingsStoreInstance.settings.selectedKnowledgeBases,
        selectedFiles: useSettingsStoreInstance.settings.selectedFiles,
        pendingSuggestionKbIds: pendingSuggestionKnowledgeBaseIds,
        mentionedItems,
      });
      ({ kbIds, knowledgeIds, tagIds, hostScope });`,
      {
        props: { embeddedMode: true, hostScope: () => ({ knowledgeBaseIds: [], pageContext: { knowledge_base_id: 'page-kb' } }) },
        useSettingsStoreInstance: { settings: { selectedKnowledgeBases: ['unrelated-sidebar-kb'], selectedFiles: ['unrelated-doc'] } },
        pendingSuggestionKnowledgeBaseIds: [], mentionedItems,
      },
    )
    assert.deepEqual(Array.from(result.kbIds), mentionedItems.filter(i => i.type === 'kb').map(i => i.id))
    assert.deepEqual(Array.from(result.knowledgeIds), mentionedItems.filter(i => i.type === 'file').map(i => i.id))
    assert.deepEqual(Array.from(result.tagIds), mentionedItems.filter(i => i.type === 'tag').map(i => i.id))
    assert.equal(result.hostScope.pageContext.knowledge_base_id, 'page-kb')
  }
})
