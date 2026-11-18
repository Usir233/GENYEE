export const domPurifyForbidTags = [
  'script',
  'style',
  'iframe',
  'frame',
  'frameset',
  'object',
  'embed',
  'applet',
  'form',
  'input',
  'textarea',
  'select',
  'base',
  'meta',
  'link',
  'foreignobject',
] as const;
export const domPurifyForbidAttr = ['onerror', 'onload', 'onclick', 'onmouseover', 'onfocus', 'onblur'] as const;

export const domPurifyAllowedUriRegexp =
  /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|cid|xmpp|blob):|data:image\/(?:png|gif|jpe?g|webp|bmp|avif|x-icon|vnd\.microsoft\.icon)[;,]|(?:resource|storage|local|minio|cos|tos|s3|oss|ks3|obs):|[^a-z]|[a-z+.-]+(?:[^a-z+.:-]|$))/i;

/** Shared DOMPurify security options (FORBID_*, URI scheme, DOM flags). */
export const domPurifySecurityOptions = {
  ALLOWED_URI_REGEXP: domPurifyAllowedUriRegexp,
  FORBID_TAGS: [...domPurifyForbidTags],
  FORBID_ATTR: [...domPurifyForbidAttr],
  KEEP_CONTENT: true,
  RETURN_DOM: false,
  RETURN_DOM_FRAGMENT: false,
  RETURN_DOM_IMPORT: false,
  SANITIZE_DOM: true,
  SANITIZE_NAMED_PROPS: true,
  WHOLE_DOCUMENT: false,
} as const;

/** Shared DOMPurify hooks: strip scripts/event attrs; noopener links; alt on images. */
export const domPurifySecurityHooks = {
  beforeSanitizeElements: (currentNode: Node) => {
    if (!('tagName' in currentNode) || !('hasAttribute' in currentNode)) return;
    const element = currentNode as Element;
    const tag = String(element.tagName || '').toLowerCase();
    if (tag === 'script' || tag === 'foreignobject') {
      if ('remove' in element && typeof element.remove === 'function') {
        element.remove();
      }
      return;
    }
    domPurifyForbidAttr.forEach((attr) => {
      if (element.hasAttribute(attr)) {
        element.removeAttribute(attr);
      }
    });
    if ('attributes' in element && element.attributes) {
      for (const attr of Array.from(element.attributes)) {
        if (attr?.name && attr.name.toLowerCase().startsWith('on')) {
          element.removeAttribute(attr.name);
        }
      }
    }
    // SVG <use> may only reference same-document #fragment IDs, never data: or external SVG documents.
    if (tag === 'use' && 'getAttribute' in element) {
      for (const attrName of ['href', 'xlink:href']) {
        const val = element.getAttribute(attrName);
        if (val !== null && !val.trim().startsWith('#')) {
          element.removeAttribute(attrName);
        }
      }
    }
    if ((tag === 'a' || tag === 'area') && 'getAttribute' in element) {
      for (const attrName of ['href', 'xlink:href']) {
        const val = element.getAttribute(attrName);
        if (val !== null && /^\s*data:/i.test(val)) {
          element.removeAttribute(attrName);
        }
      }
    }
    // target is allowed only so the afterSanitizeElements hook can add
    // target="_blank" together with rel="noopener noreferrer"; never keep an
    // author-supplied value (SVG <a>, <area>, non-http hrefs skip the hook).
    if (element.hasAttribute('target')) {
      element.removeAttribute('target');
    }
  },
  afterSanitizeElements: (currentNode: Node) => {
    if (!('tagName' in currentNode) || !('getAttribute' in currentNode)) return;
    const element = currentNode as Element;
    const tag = String(element.tagName || '').toLowerCase();
    if (tag === 'a' || tag === 'area') {
      const href = (element.getAttribute('href') || element.getAttribute('xlink:href') || '').trim();
      if (/^(?:https?:|\/\/)/i.test(href)) {
        element.setAttribute('rel', 'noopener noreferrer');
        element.setAttribute('target', '_blank');
      }
    }
    if (tag === 'img' && !element.getAttribute('alt')) {
      element.setAttribute('alt', '');
    }
  },
};

/** Chat markdown links must never replace the conversation page. */
export const markdownDomPurifySecurityHooks = {
  ...domPurifySecurityHooks,
  afterSanitizeElements: (currentNode: Node) => {
    domPurifySecurityHooks.afterSanitizeElements(currentNode);
    if (!('tagName' in currentNode) || !('getAttribute' in currentNode)) return;
    const element = currentNode as Element;
    const tag = String(element.tagName || '').toLowerCase();
    if (tag === 'a' && (element.getAttribute('href') || element.getAttribute('xlink:href'))) {
      const className = element.getAttribute('class') || '';
      if (/\bprotected-resource-card\b/.test(className) || element.hasAttribute('download')) {
        element.removeAttribute('target');
        return;
      }
      element.setAttribute('rel', 'noopener noreferrer');
      element.setAttribute('target', '_blank');
    }
  },
};

/** Shared DOMPurify config for chat / dev markdown (KaTeX + Mermaid SVG). */
export const markdownDomPurifyConfig = {
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'u', 'code', 'pre', 'ul', 'ol', 'li', 'blockquote',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'a', 'span', 'table', 'thead', 'tbody',
    'tr', 'th', 'td', 'img', 'figure', 'figcaption', 'div',
    'svg', 'g', 'path', 'rect', 'circle', 'ellipse', 'line', 'polygon',
    'polyline', 'text', 'tspan', 'defs', 'marker', 'filter', 'use',
    'clippath', 'lineargradient', 'radialgradient', 'stop', 'pattern',
    'image', 'desc', 'title', 'switch', 'symbol', 'mask',
    'math', 'annotation', 'semantics', 'mo', 'mi', 'mn', 'msup', 'mrow', 'mfrac', 'msqrt', 'mroot', 'mstyle',
    'button',
  ],
  ALLOWED_ATTR: [
    'href', 'title', 'target', 'rel', 'data-tooltip', 'data-url', 'data-kb-id',
    'data-chunk-id', 'data-doc', 'data-slug', 'data-entity-kind', 'data-entity-id', 'class', 'role', 'tabindex', 'src', 'alt', 'data-protected-src', 'data-img-loading',
    'data-artifact-index', 'data-protected-resource', 'download',
    'width', 'height', 'style', 'id', 'type', 'aria-label', 'data-mermaid', 'disabled',
    'd', 'fill', 'stroke', 'stroke-width', 'stroke-linecap', 'stroke-linejoin',
    'stroke-dasharray', 'stroke-dashoffset', 'stroke-miterlimit', 'stroke-opacity',
    'fill-opacity', 'opacity', 'transform', 'viewbox', 'preserveaspectratio',
    'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'rx', 'ry', 'r',
    'dx', 'dy', 'text-anchor', 'dominant-baseline', 'font-family', 'font-size',
    'font-weight', 'font-style', 'letter-spacing', 'word-spacing',
    'marker-start', 'marker-mid', 'marker-end', 'markerunits', 'markerwidth',
    'markerheight', 'refx', 'refy', 'orient', 'points', 'offset',
    'gradientunits', 'gradienttransform', 'spreadmethod', 'stop-color', 'stop-opacity',
    'patternunits', 'patterntransform', 'clippathunits', 'maskunits',
    'filterunits', 'primitiveunits', 'xmlns', 'xmlns:xlink', 'xlink:href',
    'version', 'baseprofile', 'enable-background', 'overflow', 'visibility',
    'display', 'pointer-events', 'cursor', 'data-emit', 'direction',
    'mathvariant', 'encoding', 'aria-hidden',
  ],
  // Do NOT set USE_PROFILES alongside ALLOWED_TAGS/ALLOWED_ATTR: DOMPurify's
  // USE_PROFILES overwrites custom minimal allowlists with its full profile set.
  ADD_ATTR: ['target'],
  ...domPurifySecurityOptions,
};
