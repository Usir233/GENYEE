import assert from 'node:assert/strict'
import test from 'node:test'
import { inlineUploadsFitRequest, jsonByteLength } from './inlineUploadSize.ts'

const MiB = 1024 * 1024
const file = (size: number, name = '文件.pdf') => ({ size, name, type: 'application/pdf' })

test('rejects Base64 uploads that fit as raw files but exceed the default HTTP limit', () => {
  assert.equal(inlineUploadsFitRequest([], [file(20 * MiB), file(20 * MiB)], 50 * MiB), false)
  assert.equal(inlineUploadsFitRequest([], [file(15 * MiB), file(15 * MiB)], 50 * MiB), true)
})

test('counts mixed images, UTF-8 query bytes, JSON escaping and configured limits', () => {
  assert.equal(inlineUploadsFitRequest([file(10 * MiB)], [file(20 * MiB)], 50 * MiB), true)
  assert.equal(inlineUploadsFitRequest([file(10 * MiB)], [file(20 * MiB)], 30 * MiB), false)
  assert.equal(inlineUploadsFitRequest([], [file(MiB)], 2 * MiB, '汉'.repeat(300_000)), false)
  assert.equal(jsonByteLength({ query: '汉\n"' }), Buffer.byteLength(JSON.stringify({ query: '汉\n"' })))
})

test('an accepted estimate bounds the actual Data URI JSON body', () => {
  const files = [file(1000), file(1001, 'quote".pdf')]
  const body = {
    query: '附件是什么？',
    images: [],
    attachment_uploads: files.map(f => ({
      data: `data:${f.type};base64,${Buffer.alloc(f.size).toString('base64')}`,
      file_name: f.name,
      file_size: f.size,
    })),
    agent_enabled: true,
    channel: 'embed',
    knowledge_base_ids: ['kb-1'],
  }
  assert.equal(inlineUploadsFitRequest([], files, 20_000, body.query), true)
  assert.ok(jsonByteLength(body) < 20_000)
})
