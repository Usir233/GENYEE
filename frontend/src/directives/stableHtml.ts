import type { ObjectDirective } from 'vue';

const DANGEROUS_TAGS = new Set(['SCRIPT', 'IFRAME', 'OBJECT', 'EMBED']);
const HREF_ATTRS = new Set(['href', 'xlink:href', 'formaction']);
const mermaidSourceByCanvas = new WeakMap<Element, string>();

function normalizeSchemeCheck(raw: string): string {
  return raw.replace(/[\u0000-\u0020]+/g, '').toLowerCase();
}

function isUnsafeAttribute(tagName: string, attrName: string, attrValue: string): boolean {
  const lower = attrName.toLowerCase();
  if (lower.startsWith('on')) return true;
  if (HREF_ATTRS.has(lower)) {
    const v = normalizeSchemeCheck(attrValue);
    return v.startsWith('javascript:') || v.startsWith('vbscript:') || v.startsWith('data:');
  }
  if (lower === 'src') {
    const v = normalizeSchemeCheck(attrValue);
    if (v.startsWith('javascript:') || v.startsWith('vbscript:')) return true;
    if (v.startsWith('data:') && !(tagName === 'IMG' && v.startsWith('data:image/'))) return true;
  }
  return false;
}

function scrubDangerousSubtree(root: ParentNode): void {
  const children = Array.from(root.childNodes);
  for (const child of children) {
    if (child.nodeType !== Node.ELEMENT_NODE) continue;
    const el = child as Element;
    const tag = el.tagName.toUpperCase();
    if (DANGEROUS_TAGS.has(tag)) {
      root.removeChild(el);
      continue;
    }
    for (const { name, value } of Array.from(el.attributes)) {
      if (isUnsafeAttribute(tag, name, value)) {
        el.removeAttribute(name);
      }
    }
    if (isMermaidCanvas(el) && !mermaidCanvasHasSvg(el)) {
      mermaidSourceByCanvas.set(el, (el.textContent || '').trim());
    }
    scrubDangerousSubtree(el);
  }
}

function imageIdentity(img: HTMLImageElement): string {
  return (img.getAttribute('data-protected-src') || img.currentSrc || img.src || '').trim();
}

function canMorph(current: Node, next: Node): boolean {
  if (current.nodeType !== next.nodeType) return false;
  if (current.nodeType !== Node.ELEMENT_NODE) return true;
  return (current as Element).tagName === (next as Element).tagName;
}

function syncAttributes(current: Element, next: Element, preserved: Set<string> = new Set()): void {
  Array.from(current.attributes).forEach(({ name }) => {
    if (!preserved.has(name) && !next.hasAttribute(name)) current.removeAttribute(name);
  });
  Array.from(next.attributes).forEach(({ name, value }) => {
    if (!preserved.has(name) && current.getAttribute(name) !== value) {
      current.setAttribute(name, value);
    }
  });
}

function morphImage(current: HTMLImageElement, next: HTMLImageElement): void {
  const currentProtected = current.getAttribute('data-protected-src');
  const nextProtected = next.getAttribute('data-protected-src');
  const sameProtectedSource = Boolean(
    currentProtected && currentProtected === nextProtected,
  );
  const sameIdentity = !currentProtected && !nextProtected && imageIdentity(current) === imageIdentity(next);
  const keepDecodedImage = current.complete
    && current.naturalWidth > 1
    && (sameProtectedSource || sameIdentity);

  if (sameProtectedSource && current.hasAttribute('data-protected-hidden')) {
    // The authenticated loader keeps missing images addressable for the final
    // retry. Do not reveal their 1px placeholder between streaming patches.
    syncAttributes(current, next, new Set(['style', 'data-protected-hidden']));
    return;
  }

  if (keepDecodedImage) {
    // Never write the placeholder src (or loading flags) over an image Chrome
    // has already decoded. More importantly, leave this exact DOM node attached
    // to the document so Chromium keeps its painted image layer.
    syncAttributes(current, next, new Set([
      'src',
      'data-protected-src',
      'data-img-loading',
      'data-auth-hydrated',
    ]));
    return;
  }

  syncAttributes(current, next);
}

function hasClass(el: Element, className: string): boolean {
  if (el.classList && typeof el.classList.contains === 'function') {
    return el.classList.contains(className);
  }
  return (el.getAttribute('class') || '').split(/\s+/).includes(className);
}

function isMermaidCanvas(el: Element): boolean {
  return hasClass(el, 'chat-mermaid-block__canvas')
    || (el.tagName.toUpperCase() === 'PRE' && hasClass(el, 'mermaid'));
}

function mermaidCanvasHasSvg(el: Element): boolean {
  if (typeof el.querySelector === 'function') return !!el.querySelector('svg');
  return el.getElementsByTagName('svg').length > 0;
}

function morphMermaidCanvas(current: Element, next: Element): void {
  const nextSource = !mermaidCanvasHasSvg(next) ? (next.textContent || '').trim() : undefined;
  // Incoming HTML from marked still has mermaid source. Keep a diagram that
  // was already painted only when the diagram source has not changed, so
  // streaming ticks do not flash it back while edits/regenerations re-render.
  if (mermaidCanvasHasSvg(current) && nextSource !== undefined) {
    const previousSource = mermaidSourceByCanvas.get(current);
    if (previousSource !== undefined && previousSource === nextSource) {
      syncAttributes(current, next, new Set(['data-mermaid']));
      return;
    }
  }
  if (nextSource !== undefined) {
    mermaidSourceByCanvas.set(current, nextSource);
  }
  syncAttributes(current, next);
  morphChildren(current, next);
}

function morphNode(current: Node, next: Node): void {
  if (current.nodeType === Node.TEXT_NODE || current.nodeType === Node.COMMENT_NODE) {
    if (current.nodeValue !== next.nodeValue) current.nodeValue = next.nodeValue;
    return;
  }

  const currentElement = current as Element;
  const nextElement = next as Element;
  if (currentElement instanceof HTMLImageElement && nextElement instanceof HTMLImageElement) {
    morphImage(currentElement, nextElement);
    return;
  }
  if (isMermaidCanvas(currentElement) && isMermaidCanvas(nextElement)) {
    morphMermaidCanvas(currentElement, nextElement);
    return;
  }

  // Standalone missing images hide their empty paragraph as well. Preserve it
  // only while the next paragraph still contains that exact image and no text.
  const currentImage = currentElement.firstElementChild;
  const nextImage = nextElement.firstElementChild;
  const keepHiddenParagraph = currentElement.tagName === 'P'
    && currentElement.hasAttribute('data-protected-hidden')
    && nextElement.children.length === 1 && !nextElement.textContent?.trim()
    && nextImage?.tagName === 'IMG'
    && Boolean(currentImage?.getAttribute('data-protected-src'))
    && currentImage?.getAttribute('data-protected-src') === nextImage?.getAttribute('data-protected-src');
  syncAttributes(currentElement, nextElement,
    keepHiddenParagraph ? new Set(['style', 'data-protected-hidden']) : undefined);
  morphChildren(currentElement, nextElement);
}

function morphChildren(currentParent: ParentNode, nextParent: ParentNode): void {
  const desiredChildren = Array.from(nextParent.childNodes);
  let current = currentParent.firstChild;

  for (const desired of desiredChildren) {
    if (current && canMorph(current, desired)) {
      const following = current.nextSibling;
      morphNode(current, desired);
      current = following;
      continue;
    }

    // Insert only the new/mismatched subtree. Existing matching ancestors and
    // their image descendants remain connected throughout streaming updates.
    const cloned = desired.cloneNode(true);
    if (cloned.nodeType === Node.ELEMENT_NODE) {
      const clonedEl = cloned as Element;
      if (isMermaidCanvas(clonedEl) && !mermaidCanvasHasSvg(clonedEl)) {
        mermaidSourceByCanvas.set(clonedEl, (clonedEl.textContent || '').trim());
      }
      scrubDangerousSubtree(clonedEl);
    }
    currentParent.insertBefore(cloned, current);
  }

  while (current) {
    const following = current.nextSibling;
    currentParent.removeChild(current);
    current = following;
  }
}

/**
 * Patch sanitized streaming HTML in place.
 *
 * Replacing innerHTML — or moving decoded images through a detached template —
 * makes Chromium briefly drop the image's painted layer on every typewriter
 * frame. This directive morphs text and surrounding markup without detaching a
 * matching loaded <img> node.
 */
export const vStableHtml: ObjectDirective<HTMLElement, string> = {
  beforeMount(el, binding) {
    const html = binding.value || '';
    const template = document.createElement('template');
    template.innerHTML = html;
    scrubDangerousSubtree(template.content);
    el.replaceChildren(...Array.from(template.content.childNodes));
  },
  updated(el, binding) {
    const html = binding.value || '';
    if (html === binding.oldValue) return;
    const template = document.createElement('template');
    template.innerHTML = html;
    scrubDangerousSubtree(template.content);
    morphChildren(el, template.content);
  },
};
