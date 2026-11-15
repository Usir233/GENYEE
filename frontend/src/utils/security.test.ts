import assert from 'node:assert/strict'
import test from 'node:test'

import {
  clearProtectedFileCache,
  documentPreviewDomPurifyConfig,
  protectProviderImageSrcInHTML,
  resolveProtectedFileCacheScope,
  setProtectedFileCacheScope,
} from './security.ts'
import { setDefaultProtectedFileAccess } from './protectedFileAccess.ts'

test('protectProviderImageSrcInHTML uses a placeholder src for provider images', () => {
  const html = '<p><img alt="preview" src="local://10000/exports/a.jpg"></p>'
  const sanitized = protectProviderImageSrcInHTML(html)
  const renderedSrc = sanitized.match(/<img[^>]*\ssrc="([^"]+)"/)?.[1]

  assert.match(renderedSrc || '', /^data:image\/gif;base64,/)
  assert.match(sanitized, /data-protected-src="local:\/\/10000\/exports\/a\.jpg"/)
})

test('protectProviderImageSrcInHTML uses a placeholder src for storage-backend images', () => {
  const html =
    '<p><img alt="preview" src="storage://c0d93536-702c-4977-aa5e-fe670073c3cb/local://10000/exports/a.png"></p>'
  const sanitized = protectProviderImageSrcInHTML(html)
  const renderedSrc = sanitized.match(/<img[^>]*\ssrc="([^"]+)"/)?.[1]

  assert.match(renderedSrc || '', /^data:image\/gif;base64,/)
  assert.match(
    sanitized,
    /data-protected-src="storage:\/\/c0d93536-702c-4977-aa5e-fe670073c3cb\/local:\/\/10000\/exports\/a\.png"/,
  )
})

test('protectProviderImageSrcInHTML uses a placeholder src for resource references', () => {
  const html = '<p><img alt="preview" src="resource://AbCdEfGhIjKlMnOpQrStUv"></p>'
  const sanitized = protectProviderImageSrcInHTML(html)
  const renderedSrc = sanitized.match(/<img[^>]*\ssrc="([^"]+)"/)?.[1]

  assert.match(renderedSrc || '', /^data:image\/gif;base64,/)
  assert.match(
    sanitized,
    /data-protected-src="resource:\/\/AbCdEfGhIjKlMnOpQrStUv"/,
  )
})

test('document preview sanitizer keeps target on links alongside image attributes and avoids USE_PROFILES override', () => {
  assert.equal('USE_PROFILES' in documentPreviewDomPurifyConfig, false)
  for (const attr of ['target', 'loading', 'decoding', 'fetchpriority']) {
    assert.ok(documentPreviewDomPurifyConfig.ADD_ATTR.includes(attr), attr)
  }
  assert.equal(documentPreviewDomPurifyConfig.ALLOWED_TAGS.includes('iframe'), false)
  assert.equal(documentPreviewDomPurifyConfig.ALLOWED_TAGS.includes('form'), false)
})

test('protected file cache scope isolates user:tenant namespaces and embed channels', () => {
  try {
    setProtectedFileCacheScope('u-1', 10001)
    assert.equal(resolveProtectedFileCacheScope(), 'u-1:10001')
    assert.equal(
      resolveProtectedFileCacheScope({ mode: 'embed', channelId: 'ch-abc', token: 'tok' }),
      'u-1:10001:embed:ch-abc',
    )
    assert.equal(
      resolveProtectedFileCacheScope({ mode: 'embed', channelId: 'ch-xyz', token: 'tok-2' }),
      'u-1:10001:embed:ch-xyz',
    )
    setDefaultProtectedFileAccess({ mode: 'embed', channelId: 'ch-default', token: 'tok-default' })
    assert.equal(resolveProtectedFileCacheScope(), 'u-1:10001:embed:ch-default')
    setDefaultProtectedFileAccess(null)
    setProtectedFileCacheScope('u-1', 20002)
    assert.equal(resolveProtectedFileCacheScope(), 'u-1:20002')
  } finally {
    setDefaultProtectedFileAccess(null)
    clearProtectedFileCache()
    setProtectedFileCacheScope(null, null)
  }
  assert.equal(resolveProtectedFileCacheScope(), 'anon:default')
})
