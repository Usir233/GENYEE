import { nextTick } from 'vue';
import { marked } from 'marked';
import markedKatex from 'marked-katex-extension';
import 'katex/dist/katex.min.css';
import hljs from 'highlight.js';
import 'highlight.js/styles/github.css';
import mermaid from 'mermaid';

import {
  sanitizeHTML,
  safeMarkdownToHTML,
  createSafeImage,
  isValidImageURL,
  hydrateProtectedFileImages,
} from '@/utils/security';
import { normalizeSpuriousTablePrefixes } from '@/utils/markdownTableNormalize';
import { stripDuplicateLeadingH1 } from '@/utils/markdownTitleDedupe';
import { ensureLiteralSingleTildeOnGlobalMarked } from '@/utils/markedLiteralTilde';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';
import type { KnowledgeChunkItem } from './types';

let markdownGlobalConfigured = false;
let mermaidRenderCount = 0;

function ensureMarkdownAndMermaidInitialized(): void {
  if (markdownGlobalConfigured) return;
  markdownGlobalConfigured = true;

  mermaid.initialize({
    startOnLoad: false,
    theme: 'default',
    securityLevel: 'strict',
    fontFamily: 'PingFang SC, Microsoft YaHei, sans-serif',
    flowchart: {
      useMaxWidth: true,
      htmlLabels: true,
      curve: 'basis',
    },
    sequence: {
      useMaxWidth: true,
      diagramMarginX: 8,
      diagramMarginY: 8,
      actorMargin: 50,
      width: 150,
      height: 65,
    },
    gantt: {
      useMaxWidth: true,
      leftPadding: 75,
      gridLineStartPadding: 35,
      barHeight: 20,
      barGap: 4,
      topPadding: 50,
    },
  });

  marked.use({
    breaks: true,
    gfm: true,
  });
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }));
  ensureLiteralSingleTildeOnGlobalMarked();
}

export const escapeCodeHtml = (raw: string): string =>
  raw.replace(
    /[&<>"']/g,
    (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch] || ch),
  );

const preprocessMathDelimiters = (rawText: string): string => {
  if (!rawText || typeof rawText !== 'string') {
    return '';
  }
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$');
};

const checkImage = (imageUrl: string): Promise<boolean> =>
  new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = imageUrl;
  });

const handleMermaidClick = (e: Event): void => {
  e.stopPropagation();
  const target = e.currentTarget as HTMLElement | null;
  const svg = target?.querySelector('svg');
  if (svg) {
    openMermaidFullscreen(svg.outerHTML);
  }
};

/**
 * Append next chunk text onto accumulated text while deduplicating overlapping spans.
 */
export const appendChunkContent = (acc: string, next: string, positionOverlap: number): string => {
  if (!acc) return next;
  if (!next) return acc;
  if (positionOverlap <= 0) return acc + next;

  const MIN_OVERLAP = 12;
  const span = Math.max(positionOverlap, 0);
  const maxK = Math.min(acc.length, next.length, Math.max(span * 3, 400));
  const headSlack = Math.max(span * 2, 320);

  for (let k = maxK; k >= MIN_OVERLAP; k--) {
    const suffix = acc.slice(acc.length - k);
    const pos = next.indexOf(suffix);
    if (pos !== -1 && pos <= headSlack) {
      return acc + next.slice(pos + k);
    }
  }
  return acc + next;
};

/**
 * Merge document chunks ordered by start_at / chunk_index into full Markdown text.
 */
export const mergeChunks = (chunks: KnowledgeChunkItem[]): string => {
  if (!chunks || chunks.length === 0) return '';

  const sortedChunks = chunks.toSorted((a, b) => {
    const startA = a.start_at ?? a.chunk_index ?? 0;
    const startB = b.start_at ?? b.chunk_index ?? 0;
    return startA - startB;
  });

  let merged = sortedChunks[0].content || '';
  let mergedEnd = sortedChunks[0].end_at ?? 0;

  for (let i = 1; i < sortedChunks.length; i++) {
    const currentChunk = sortedChunks[i];
    const currentStartAt = currentChunk.start_at ?? 0;
    const currentEndAt = currentChunk.end_at ?? 0;
    const currentContent = currentChunk.content || '';

    if (!currentContent) continue;

    if (currentStartAt > mergedEnd && mergedEnd > 0) {
      merged = `${merged}\n\n${currentContent}`;
    } else {
      const positionOverlap = mergedEnd - currentStartAt;
      merged = appendChunkContent(merged, currentContent, positionOverlap);
    }

    if (currentEndAt > mergedEnd) {
      mergedEnd = currentEndAt;
    }
  }

  return merged;
};

export function useChunkMarkdownPipeline(options: {
  t: (key: string, params?: Record<string, unknown>) => string;
  getRenderRoot: () => ParentNode | null;
  getKbId: () => string | undefined;
  getDocTitle: () => string | undefined;
}) {
  ensureMarkdownAndMermaidInitialized();

  const renderer = new marked.Renderer();

  renderer.image = ({ href, title, text }) => {
    if (!isValidImageURL(href)) {
      return `<p>${options.t('error.invalidImageLink')}</p>`;
    }
    const safeImage = createSafeImage(href, text || '', title || '');
    return `<figure>
                ${safeImage}
                <figcaption style="text-align: left;">${text || ''}</figcaption>
            </figure>`;
  };

  renderer.code = ({ text, lang }) => {
    const safeRawText = typeof text === 'string' ? text : '';

    if (lang === 'mermaid') {
      const id = `mermaid-${++mermaidRenderCount}`;
      return `<div class="mermaid" id="${id}">${escapeCodeHtml(safeRawText)}</div>`;
    }

    let detectedLang = lang;
    let highlighted = '';
    if (lang && hljs.getLanguage(lang)) {
      try {
        highlighted = hljs.highlight(safeRawText, { language: lang }).value;
      } catch {
        const auto = hljs.highlightAuto(safeRawText);
        highlighted = auto.value;
        detectedLang = auto.language || lang;
      }
    } else {
      const auto = hljs.highlightAuto(safeRawText);
      highlighted = auto.value;
      detectedLang = auto.language || lang;
    }
    const displayLang = escapeCodeHtml(String(detectedLang || 'Code'));
    const safeLangToken = String(detectedLang || '').replace(/[^a-zA-Z0-9_-]/g, '');
    return `
    <div class="code-block-wrapper">
      <div class="code-block-header">
        <span class="code-block-lang">${displayLang}</span>
      </div>
      <pre class="code-block-pre"><code class="hljs language-${safeLangToken}">${highlighted}</code></pre>
    </div>
  `;
  };

  const processMarkdown = (markdownText?: string): string => {
    if (!markdownText || typeof markdownText !== 'string') return '';

    let processedText = markdownText.replace(/^\s*---\r?\n[\s\S]*?\r?\n---\r?\n/, '');
    processedText = processedText
      .replace(/&#39;/g, "'")
      .replace(/&#x27;/gi, "'")
      .replace(/&apos;/g, "'")
      .replace(/&#34;/g, '"')
      .replace(/&#x22;/gi, '"')
      .replace(/&quot;/g, '"')
      .replace(/&lt;/g, '<')
      .replace(/&gt;/g, '>')
      .replace(/&amp;/g, '&');

    const docTitle = options.getDocTitle();
    processedText = stripDuplicateLeadingH1(processedText, docTitle ? [docTitle] : []);
    processedText = processedText.replace(/<p>\s*(\|[\s\S]*?\|)\s*<\/p>/gi, '\n$1\n');
    processedText = normalizeSpuriousTablePrefixes(processedText);

    const mathSafeText = preprocessMathDelimiters(processedText);
    const safeMarkdown = safeMarkdownToHTML(mathSafeText);
    let html = marked.parse(safeMarkdown, { renderer }) as string;
    html = html.replace(/&lt;br\s*\/?&gt;/gi, '<br>');

    return sanitizeHTML(html);
  };

  const bindMermaidClickEvents = (): void => {
    const renderRoot = options.getRenderRoot();
    if (!renderRoot) return;
    const mermaidDivs = renderRoot.querySelectorAll('.mermaid');
    mermaidDivs.forEach((div) => {
      const divEl = div as HTMLElement;
      divEl.style.cursor = 'pointer';
      divEl.removeEventListener('click', handleMermaidClick);
      divEl.addEventListener('click', handleMermaidClick);
    });
  };

  const renderMermaidDiagrams = async (): Promise<void> => {
    try {
      const mermaidElements = options.getRenderRoot()?.querySelectorAll<HTMLElement>('.mermaid');
      if (mermaidElements && mermaidElements.length > 0) {
        await mermaid.run({
          nodes: mermaidElements,
        });
        await nextTick();
        bindMermaidClickEvents();
      }
    } catch (error) {
      console.error('Mermaid rendering error:', error);
    }
  };

  const runMarkdownPostRenderPipeline = async (): Promise<void> => {
    await nextTick();
    const renderRoot = options.getRenderRoot();
    if (!renderRoot) return;

    const kbId = options.getKbId();
    await hydrateProtectedFileImages(
      renderRoot,
      kbId ? { mode: 'knowledgeBase', kbId } : undefined,
    );

    const images = renderRoot.querySelectorAll?.('img.markdown-image') as
      | NodeListOf<HTMLImageElement>
      | undefined;
    if (images) {
      images.forEach((item) => {
        void checkImage(item.src).then((isValid) => {
          if (!isValid) {
            item.remove();
          }
        });
      });
    }

    await renderMermaidDiagrams();
  };

  return {
    processMarkdown,
    runMarkdownPostRenderPipeline,
  };
}
