import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

test('ArtifactLibrary URL-encodes session_id in openSession and reloads on workspace or user switch', async (t: TestContext) => {
  const f = fixture(t)
  const pushed: string[] = []
  let listCalls = 0
  const authState = vue.reactive({
    user: { id: 'user-1' },
    effectiveTenantId: 10,
  })

  const url = new URL('./ArtifactLibrary.vue', import.meta.url)
  const page = f.component(url, {
    'vue-router': {
      useRoute: () => ({ query: {} }),
      useRouter: () => ({
        push: (target: string) => { pushed.push(target) },
        replace: () => {},
      }),
    },
    'vue-i18n': { useI18n: () => ({ t: (k: string) => k }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {} } },
    '@/stores/auth': { useAuthStore: () => authState },
    '@/api/chat': { downloadArtifact: async () => new Blob() },
    '@/api/artifacts': {
      deleteArtifactLibraryItem: async () => {},
      listArtifactLibrary: async () => {
        listCalls++
        return { data: [], total: 0 }
      },
    },
    '@/utils/artifactLibrary': {
      ARTIFACT_CATEGORIES: ['all'],
      artifactCategoryExtensions: () => [],
      groupArtifactsByDate: () => [],
      parseArtifactCategory: () => 'all',
    },
    '@/utils/sessionArtifacts': {
      formatArtifactDateTime: () => '',
      formatArtifactSize: () => '',
    },
    '@/utils/filePreview': { resolveFilePreviewExt: () => 'html' },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => () => {} },
    '@/components/EmptyState.vue': { default: {} },
    '@/components/icons/ResourceIcon.vue': { default: {} },
    '@/components/document-preview.vue': { default: {} },
    '@/views/chat/components/ArtifactFileIcon.vue': { default: {} },
  }, {})

  await flush()
  assert.equal(listCalls, 1)

  page.vm.openPreview({
    session_id: 'sess/1?foo=bar#hash',
    message_id: 'msg-1',
    index: 0,
    file_name: 'report.html',
    file_type: 'html',
  })
  assert.equal(page.vm.previewVisible, true)

  page.vm.openSession({
    session_id: 'sess/1?foo=bar#hash',
    message_id: 'msg-1',
    index: 0,
    file_name: 'report.html',
    file_type: 'html',
  })
  assert.equal(pushed[0], `/platform/chat/${encodeURIComponent('sess/1?foo=bar#hash')}`)

  page.vm.openPreview({
    session_id: 'sess-2',
    message_id: 'msg-2',
    index: 0,
    file_name: 'report.html',
    file_type: 'html',
  })
  assert.equal(page.vm.previewVisible, true)

  authState.effectiveTenantId = 20
  await flush()
  assert.equal(page.vm.previewVisible, false, 'switching workspace must close open artifact preview')
  assert.equal(listCalls, 2, 'switching workspace must reload artifact list')
})
