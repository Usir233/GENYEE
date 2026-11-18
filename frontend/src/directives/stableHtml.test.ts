import assert from 'node:assert/strict'
import test from 'node:test'
import { DOMParser } from '@xmldom/xmldom'
import { vStableHtml } from './stableHtml.ts'
import { clearProtectedFileFailureCache, hydrateProtectedFileImages } from '../utils/security.ts'

// Use real parsed trees and the complete directive. Adapt the small browser
// surface absent from xmldom rather than extracting the morph implementation.
function parse(html: string): any {
  const root = new DOMParser().parseFromString(`<div>${html}</div>`, 'text/xml').documentElement!
  function adapt(el: any) {
    const tag = el.tagName.toUpperCase()
    Object.defineProperties(el, {
      tagName: { value: tag },
      children: { get: () => Array.from(el.childNodes).filter((child: any) => child.nodeType === 1) },
      firstElementChild: { get: () => el.children[0] ?? null },
      parentElement: { get: () => el.parentNode?.nodeType === 1 ? el.parentNode : null },
      src: { get: () => el.getAttribute('src'), set: value => el.setAttribute('src', value) },
      alt: { get: () => el.getAttribute('alt') },
      innerHTML: {
        set: (value: string) => {
          while (el.firstChild) el.removeChild(el.firstChild)
          const parsed = parse(value)
          while (parsed.firstChild) el.appendChild(parsed.firstChild)
        },
      },
    })
    el.replaceChildren = (...nodes: any[]) => {
      while (el.firstChild) el.removeChild(el.firstChild)
      for (const node of nodes) el.appendChild(node)
    }
    el.complete = true
    el.naturalWidth = 1
    el.classList = { contains: (name: string) => (el.getAttribute('class') || '').split(/\s+/).includes(name) }
    el.dataset = new Proxy({}, {
      get: (_, key: string) => el.getAttribute(`data-${key.replace(/[A-Z]/g, char => `-${char.toLowerCase()}`)}`),
      set: (_, key: string, value) => {
        el.setAttribute(`data-${key.replace(/[A-Z]/g, char => `-${char.toLowerCase()}`)}`, value)
        return true
      },
    })
    el.style = {
      get display() { return el.getAttribute('style')?.match(/(?:^|;)\s*display:\s*([^;]+)/)?.[1] || '' },
      set display(value: string) {
        const rest = (el.getAttribute('style') || '').replace(/(?:^|;)\s*display:[^;]*;?/g, '')
        el.setAttribute('style', `${rest}${value ? `;display:${value}` : ''}`)
      },
    }
    el.querySelector = (selector: string) => {
      if (selector === 'svg') return el.getElementsByTagName('svg')[0] ?? null
      return null
    }
    el.querySelectorAll = (selector: string) => Array.from(el.getElementsByTagName('img'))
      .filter((img: any) => selector.includes('src^=') || img.hasAttribute('data-protected-src'))
    for (const child of el.children) adapt(child)
  }
  adapt(root)
  return root
}

type DirectiveHook = (el: any, binding: { value: string; oldValue?: string }) => void
const runBeforeMount = vStableHtml.beforeMount as unknown as DirectiveHook
const runUpdated = vStableHtml.updated as unknown as DirectiveHook

test('missing image and paragraph stay hidden across streaming morphs, then recover without new HTML', async t => {
  const previous = {
    Node: (globalThis as any).Node, HTMLImageElement: (globalThis as any).HTMLImageElement,
    document: (globalThis as any).document, window: (globalThis as any).window, fetch: globalThis.fetch,
  }
  Object.assign(globalThis, {
    Node: { ELEMENT_NODE: 1, TEXT_NODE: 3, COMMENT_NODE: 8 },
    HTMLImageElement: class { static [Symbol.hasInstance](el: any) { return el?.tagName === 'IMG' } },
    document: {
      createElement(tag: string) {
        assert.equal(tag, 'template')
        return { content: null, set innerHTML(html: string) { this.content = parse(html) } }
      },
    },
    window: { location: { origin: 'http://localhost' }, addEventListener() {} },
  })
  t.after(() => { Object.assign(globalThis, previous); clearProtectedFileFailureCache() })
  const source = 'local://2/exports/morph-hidden.png'
  const imageHTML = `<p><img src="${source}" data-protected-src="${source}" style="max-width:80%" /></p>`
  const root = parse(`${imageHTML}<p>first</p>`)
  const img = root.children[0].firstElementChild
  const paragraph = img.parentElement
  const access = { mode: 'message', sessionId: 'session', messageId: 'assistant' } as const
  let persisted = false
  globalThis.fetch = async () => persisted
    ? new Response(new Blob(['png'], { type: 'image/png' })) : new Response(null, { status: 404 })
  await hydrateProtectedFileImages(root, access)
  assert.equal(img.style.display, 'none')
  assert.equal(paragraph.style.display, 'none')
  let previousHTML = `${imageHTML}<p>first</p>`
  for (const text of ['first second', 'first second third']) {
    const html = `${imageHTML}<p>${text}</p>`
    runUpdated(root, { value: html, oldValue: previousHTML })
    // Assert BEFORE another onUpdated/hydrate pass can mask a flicker.
    assert.equal(root.children[0].firstElementChild, img)
    assert.equal(img.style.display, 'none')
    assert.equal(paragraph.style.display, 'none')
    previousHTML = html
  }
  persisted = true
  clearProtectedFileFailureCache()
  await hydrateProtectedFileImages(root, access)
  assert.match(img.src, /^blob:/)
  assert.equal(img.style.display, '')
  assert.equal(paragraph.style.display, '')
  assert.equal(img.hasAttribute('data-protected-hidden'), false)
  assert.equal(paragraph.hasAttribute('data-protected-hidden'), false)

  // A new source occupying the same paragraph must not inherit hidden state.
  persisted = false
  const other = 'local://2/exports/morph-other.png'
  const otherHTML = imageHTML.replaceAll(source, other)
  runUpdated(root, { value: `${otherHTML}<p>changed</p>`, oldValue: previousHTML })
  await hydrateProtectedFileImages(root, access)
  assert.equal(img.style.display, 'none')
  runUpdated(root, { value: `${imageHTML}<p>restored</p>`, oldValue: `${otherHTML}<p>changed</p>` })
  assert.notEqual(img.style.display, 'none')
  assert.notEqual(paragraph.style.display, 'none')
  assert.equal(img.hasAttribute('data-protected-hidden'), false)
})

test('decoded protected image with identical alt updates when data-protected-src changes', t => {
  const previous = {
    Node: (globalThis as any).Node, HTMLImageElement: (globalThis as any).HTMLImageElement,
    document: (globalThis as any).document,
  }
  Object.assign(globalThis, {
    Node: { ELEMENT_NODE: 1, TEXT_NODE: 3, COMMENT_NODE: 8 },
    HTMLImageElement: class { static [Symbol.hasInstance](el: any) { return el?.tagName === 'IMG' } },
    document: {
      createElement(tag: string) {
        assert.equal(tag, 'template')
        return { content: null, set innerHTML(html: string) { this.content = parse(html) } }
      },
    },
  })
  t.after(() => Object.assign(globalThis, previous))

  const firstHtml = '<p><img src="local://1/a.png" data-protected-src="local://1/a.png" alt="Chart" /></p>'
  const root = parse('<p></p>')
  runBeforeMount(root, { value: firstHtml })
  // Simulate hydration + decoding on the live image element
  const img = root.children[0].firstElementChild
  img.setAttribute('src', 'blob:http://localhost/decoded-a')
  img.setAttribute('data-auth-hydrated', 'local://1/a.png')
  img.complete = true
  img.naturalWidth = 640

  // Streaming patch with SAME source keeps decoded blob
  const sameSourcePatch = '<p><img src="local://1/a.png" data-protected-src="local://1/a.png" alt="Chart" /><span>more</span></p>'
  runUpdated(root, { value: sameSourcePatch, oldValue: firstHtml })
  assert.equal(img.getAttribute('src'), 'blob:http://localhost/decoded-a')
  assert.equal(img.getAttribute('data-protected-src'), 'local://1/a.png')

  // Regenerated / edited HTML with DIFFERENT data-protected-src and SAME alt MUST update!
  const secondHtml = '<p><img src="local://1/b.png" data-protected-src="local://1/b.png" alt="Chart" /></p>'
  runUpdated(root, { value: secondHtml, oldValue: sameSourcePatch })
  assert.equal(img.getAttribute('data-protected-src'), 'local://1/b.png')
  assert.equal(img.getAttribute('src'), 'local://1/b.png')
  assert.equal(img.hasAttribute('data-auth-hydrated'), false)
})

test('mermaid canvas keeps painted svg during streaming ticks but invalidates when source changes', t => {
  const previous = {
    Node: (globalThis as any).Node, HTMLImageElement: (globalThis as any).HTMLImageElement,
    document: (globalThis as any).document,
  }
  Object.assign(globalThis, {
    Node: { ELEMENT_NODE: 1, TEXT_NODE: 3, COMMENT_NODE: 8 },
    HTMLImageElement: class { static [Symbol.hasInstance](el: any) { return el?.tagName === 'IMG' } },
    document: {
      createElement(tag: string) {
        assert.equal(tag, 'template')
        return { content: null, set innerHTML(html: string) { this.content = parse(html) } }
      },
    },
  })
  t.after(() => Object.assign(globalThis, previous))

  const htmlA1 = '<div class="chat-mermaid-block__canvas" data-mermaid="false">graph TD; A--&gt;B</div><p>tick1</p>'
  const root = parse('<div></div>')
  runBeforeMount(root, { value: htmlA1 })

  // Simulate Mermaid enhancer replacing text with <svg> and setting data-mermaid="true"
  const canvas = root.children[0]
  const svgDoc = parse('<svg id="diagram-a"></svg>')
  while (canvas.firstChild) canvas.removeChild(canvas.firstChild)
  canvas.appendChild(svgDoc.children[0])
  canvas.setAttribute('data-mermaid', 'true')

  // Same Mermaid source on subsequent streaming tick keeps painted <svg>
  const htmlA2 = '<div class="chat-mermaid-block__canvas" data-mermaid="false">graph TD; A--&gt;B</div><p>tick2</p>'
  runUpdated(root, { value: htmlA2, oldValue: htmlA1 })
  assert.equal(canvas.getAttribute('data-mermaid'), 'true')
  assert.ok(canvas.querySelector('svg'))

  // Different Mermaid source invalidates stale <svg> so enhancer re-renders new diagram
  const htmlB = '<div class="chat-mermaid-block__canvas" data-mermaid="false">graph TD; X--&gt;Y</div><p>tick3</p>'
  runUpdated(root, { value: htmlB, oldValue: htmlA2 })
  assert.equal(canvas.getAttribute('data-mermaid'), 'false')
  assert.equal(canvas.querySelector('svg'), null)
  assert.match(canvas.textContent, /X-->Y/)
})

test('vStableHtml strips dangerous elements, event handlers, and javascript URIs', t => {
  const previous = {
    Node: (globalThis as any).Node, HTMLImageElement: (globalThis as any).HTMLImageElement,
    document: (globalThis as any).document,
  }
  Object.assign(globalThis, {
    Node: { ELEMENT_NODE: 1, TEXT_NODE: 3, COMMENT_NODE: 8 },
    HTMLImageElement: class { static [Symbol.hasInstance](el: any) { return el?.tagName === 'IMG' } },
    document: {
      createElement(tag: string) {
        assert.equal(tag, 'template')
        return { content: null, set innerHTML(html: string) { this.content = parse(html) } }
      },
    },
  })
  t.after(() => Object.assign(globalThis, previous))

  const root = parse('<div></div>')
  const dirty = '<p onclick="evil()">safe<script>evil()</script><a href="javascript:alert(1)">bad</a></p>'
  runBeforeMount(root, { value: dirty })
  assert.equal(root.getElementsByTagName('script').length, 0)
  assert.equal(root.children[0].hasAttribute('onclick'), false)
  assert.equal(root.getElementsByTagName('a')[0].hasAttribute('href'), false)

  const dirtyUpdate = '<p onload="evil()">updated<iframe src="https://evil.example"></iframe><a href="data:text/html,boom">bad2</a></p>'
  runUpdated(root, { value: dirtyUpdate, oldValue: dirty })
  assert.equal(root.getElementsByTagName('iframe').length, 0)
  assert.equal(root.children[0].hasAttribute('onload'), false)
  assert.equal(root.getElementsByTagName('a')[0].hasAttribute('href'), false)
})
