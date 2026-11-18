import JSZip from 'jszip';
import i18n from '../i18n/index.ts';
import { sanitizeSvgForDataUrl } from './security';

function defaultCellImageAlt(): string {
  return i18n.global.t('preview.xlsxCellImageAlt');
}

export interface XlsxCellImage {
  src: string;
  alt: string;
}

export type SheetCellImageMap = Map<string, Map<string, XlsxCellImage[]>>;

const MIME_BY_EXT: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  bmp: 'image/bmp',
  svg: 'image/svg+xml',
  tif: 'image/tiff',
  tiff: 'image/tiff',
};

function isZipHeader(buffer: ArrayBuffer): boolean {
  if (buffer.byteLength < 4) return false;
  const view = new Uint8Array(buffer, 0, 4);
  return view[0] === 0x50 && view[1] === 0x4b && view[2] === 0x03 && view[3] === 0x04;
}

function normalizeZipPath(baseDir: string, target: string): string {
  const cleaned = target.replace(/\\/g, '/').trim();
  if (cleaned.startsWith('/')) {
    return cleaned.replace(/^\/+/, '');
  }
  const parts = (baseDir ? `${baseDir}/${cleaned}` : cleaned).split('/');
  const resolved: string[] = [];
  for (const part of parts) {
    if (!part || part === '.') continue;
    if (part === '..') {
      resolved.pop();
    } else {
      resolved.push(part);
    }
  }
  return resolved.join('/');
}

function dirOf(path: string): string {
  const idx = path.lastIndexOf('/');
  return idx >= 0 ? path.slice(0, idx) : '';
}

function relsPathFor(partPath: string): string {
  const dir = dirOf(partPath);
  const base = partPath.slice(dir ? dir.length + 1 : 0);
  return dir ? `${dir}/_rels/${base}.rels` : `_rels/${base}.rels`;
}

function colIndexToLetter(colIdx0: number): string {
  let n = colIdx0 + 1;
  let letters = '';
  while (n > 0) {
    const rem = (n - 1) % 26;
    letters = String.fromCharCode(65 + rem) + letters;
    n = Math.floor((n - 1) / 26);
  }
  return letters;
}

function parseRelsXml(xml: string, ownerPartPath: string): Map<string, string> {
  const map = new Map<string, string>();
  const baseDir = dirOf(ownerPartPath);
  const relRegex = /<(?:\w+:)?Relationship\b([^>]*?)\/?>/gi;
  let m: RegExpExecArray | null;
  while ((m = relRegex.exec(xml)) !== null) {
    const attrs = m[1];
    const idMatch = /\bId\s*=\s*["']([^"']+)["']/i.exec(attrs);
    const targetMatch = /\bTarget\s*=\s*["']([^"']+)["']/i.exec(attrs);
    if (idMatch && targetMatch) {
      map.set(idMatch[1], normalizeZipPath(baseDir, targetMatch[1]));
    }
  }
  return map;
}

async function readZipText(zip: JSZip, path: string): Promise<string | null> {
  const file = zip.file(path);
  if (!file) return null;
  return file.async('string');
}

function escapeHtmlAttr(val: string): string {
  return val
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

/**
 * Extract embedded cell & drawing images from an .xlsx ArrayBuffer, keyed by
 * sheet name -> cell reference (e.g. "A2", "C2") -> array of { src, alt }.
 */
export async function extractXlsxCellImages(arrayBuffer: ArrayBuffer): Promise<SheetCellImageMap> {
  const result: SheetCellImageMap = new Map();
  if (!isZipHeader(arrayBuffer)) return result;

  let zip: JSZip;
  try {
    zip = await JSZip.loadAsync(arrayBuffer);
  } catch {
    return result;
  }

  // 1. Load all supported xl/media/* entries as data URLs
  const mediaDataUrls = new Map<string, string>();
  const mediaEntries = Object.keys(zip.files).filter(
    (name) => name.toLowerCase().startsWith('xl/media/') && !zip.files[name].dir,
  );
  if (mediaEntries.length === 0) return result;

  await Promise.all(
    mediaEntries.map(async (name) => {
      const ext = (name.split('.').pop() || '').toLowerCase();
      const mime = MIME_BY_EXT[ext];
      if (!mime) return;
      try {
        if (ext === 'svg') {
          const svgText = await zip.files[name].async('string');
          const safeUrl = sanitizeSvgForDataUrl(svgText);
          if (safeUrl) {
            mediaDataUrls.set(name, safeUrl);
          }
          return;
        }
        const b64 = await zip.files[name].async('base64');
        if (b64) {
          mediaDataUrls.set(name, `data:${mime};base64,${b64}`);
        }
      } catch {
        // Ignore unreadable media entry
      }
    }),
  );
  if (mediaDataUrls.size === 0) return result;

  // 2. Parse workbook sheet list & relationships
  const workbookXml = await readZipText(zip, 'xl/workbook.xml');
  const workbookRelsXml = await readZipText(zip, 'xl/_rels/workbook.xml.rels');
  const wbRels = workbookRelsXml ? parseRelsXml(workbookRelsXml, 'xl/workbook.xml') : new Map<string, string>();

  const sheetPathByName = new Map<string, string>();
  if (workbookXml) {
    const sheetRegex = /<(?:\w+:)?sheet\b([^>]*?)\/?>/gi;
    let m: RegExpExecArray | null;
    let idx = 1;
    while ((m = sheetRegex.exec(workbookXml)) !== null) {
      const attrs = m[1];
      const nameMatch = /\bname\s*=\s*["']([^"']+)["']/i.exec(attrs);
      const rIdMatch = /\b(?:\w+:)?id\s*=\s*["']([^"']+)["']/i.exec(attrs);
      const sheetName = nameMatch ? nameMatch[1] : `Sheet${idx}`;
      const rId = rIdMatch ? rIdMatch[1] : '';
      const target = (rId && wbRels.get(rId)) || `xl/worksheets/sheet${idx}.xml`;
      sheetPathByName.set(sheetName, target);
      idx++;
    }
  }

  // 3. Parse Office 365 RichData chain:
  //    richValueRel.xml.rels -> richValueRel.xml -> rdrichvalue.xml -> metadata.xml
  const vmToMedia = new Map<number, XlsxCellImage>();
  const richRelRelsXml = await readZipText(zip, 'xl/richData/_rels/richValueRel.xml.rels');
  const richRelXml = await readZipText(zip, 'xl/richData/richValueRel.xml');
  if (richRelRelsXml && richRelXml) {
    const richRelMap = parseRelsXml(richRelRelsXml, 'xl/richData/richValueRel.xml');
    const relTargets: string[] = [];
    const relTagRegex = /<(?:\w+:)?rel\b([^>]*?)\/?>/gi;
    let m: RegExpExecArray | null;
    while ((m = relTagRegex.exec(richRelXml)) !== null) {
      const rIdMatch = /\b(?:\w+:)?id\s*=\s*["']([^"']+)["']/i.exec(m[1]);
      const target = rIdMatch ? richRelMap.get(rIdMatch[1]) || '' : '';
      relTargets.push(target);
    }

    // Parse rdrichvalue.xml if present
    const rvToMedia: { mediaPath: string; alt: string }[] = [];
    const rdRichValXml =
      (await readZipText(zip, 'xl/richData/rdrichvalue.xml')) ||
      (await readZipText(zip, 'xl/richData/rdRichValue.xml'));
    if (rdRichValXml) {
      const rvBlockRegex = /<(?:\w+:)?rv\b[^>]*>([\s\S]*?)<\/(?:\w+:)?rv>/gi;
      let rvMatch: RegExpExecArray | null;
      while ((rvMatch = rvBlockRegex.exec(rdRichValXml)) !== null) {
        const inner = rvMatch[1];
        const vValues: string[] = [];
        const vRegex = /<(?:\w+:)?v\b[^>]*>([\s\S]*?)<\/(?:\w+:)?v>/gi;
        let vMatch: RegExpExecArray | null;
        while ((vMatch = vRegex.exec(inner)) !== null) {
          vValues.push(vMatch[1].trim());
        }
        const relIdx = vValues.length > 0 ? parseInt(vValues[0], 10) : NaN;
        const mediaPath = !Number.isNaN(relIdx) && relIdx >= 0 && relIdx < relTargets.length ? relTargets[relIdx] : '';
        const altCandidate = vValues.find((v, i) => i > 0 && Number.isNaN(Number(v))) || '';
        rvToMedia.push({ mediaPath, alt: altCandidate });
      }
    } else {
      for (const target of relTargets) {
        rvToMedia.push({ mediaPath: target, alt: '' });
      }
    }

    // Parse xl/metadata.xml <valueMetadata> -> 1-based vm index
    const metadataXml = await readZipText(zip, 'xl/metadata.xml');
    let mappedFromMetadata = false;
    if (metadataXml) {
      const valMetaMatch = /<(?:\w+:)?valueMetadata\b[^>]*>([\s\S]*?)<\/(?:\w+:)?valueMetadata>/i.exec(metadataXml);
      if (valMetaMatch) {
        const bkRegex = /<(?:\w+:)?bk\b[^>]*>([\s\S]*?)<\/(?:\w+:)?bk>/gi;
        let bkMatch: RegExpExecArray | null;
        let vmIndex = 1;
        while ((bkMatch = bkRegex.exec(valMetaMatch[1])) !== null) {
          const rcMatch = /<(?:\w+:)?rc\b[^>]*?\bv\s*=\s*["'](\d+)["'][^>]*\/?>/i.exec(bkMatch[1]);
          if (rcMatch) {
            const rvIdx = parseInt(rcMatch[1], 10);
            const entry = rvToMedia[rvIdx];
            const src = entry?.mediaPath ? mediaDataUrls.get(entry.mediaPath) : undefined;
            if (src) {
              vmToMedia.set(vmIndex, { src, alt: entry.alt || defaultCellImageAlt() });
              mappedFromMetadata = true;
            }
          }
          vmIndex++;
        }
      }
    }
    if (!mappedFromMetadata) {
      rvToMedia.forEach((entry, idx) => {
        const src = entry.mediaPath ? mediaDataUrls.get(entry.mediaPath) : undefined;
        if (src) {
          vmToMedia.set(idx + 1, { src, alt: entry.alt || defaultCellImageAlt() });
          vmToMedia.set(idx, { src, alt: entry.alt || defaultCellImageAlt() });
        }
      });
    }
  }

  // 4. Parse WPS xl/cellimages.xml (DISPIMG ID -> image)
  const dispImgToMedia = new Map<string, XlsxCellImage>();
  const cellImagesXml = await readZipText(zip, 'xl/cellimages.xml');
  const cellImagesRelsXml = await readZipText(zip, 'xl/_rels/cellimages.xml.rels');
  if (cellImagesXml && cellImagesRelsXml) {
    const ciRels = parseRelsXml(cellImagesRelsXml, 'xl/cellimages.xml');
    const ciBlockRegex = /<(?:\w+:)?cellImage\b[^>]*>([\s\S]*?)<\/(?:\w+:)?cellImage>/gi;
    let ciMatch: RegExpExecArray | null;
    while ((ciMatch = ciBlockRegex.exec(cellImagesXml)) !== null) {
      const block = ciMatch[1];
      const cNvPrMatch = /<(?:\w+:)?cNvPr\b([^>]*?)\/?>/i.exec(block);
      const blipMatch = /<(?:\w+:)?blip\b[^>]*?\b(?:\w+:)?embed\s*=\s*["']([^"']+)["']/i.exec(block);
      if (cNvPrMatch && blipMatch) {
        const nameMatch = /\bname\s*=\s*["']([^"']+)["']/i.exec(cNvPrMatch[1]);
        const descrMatch = /\bdescr\s*=\s*["']([^"']+)["']/i.exec(cNvPrMatch[1]);
        const mediaPath = ciRels.get(blipMatch[1]);
        const src = mediaPath ? mediaDataUrls.get(mediaPath) : undefined;
        if (nameMatch && src) {
          dispImgToMedia.set(nameMatch[1], { src, alt: descrMatch ? descrMatch[1] : nameMatch[1] });
        }
      }
    }
  }

  // 5. Process each worksheet for vm="...", DISPIMG("..."), and floating drawings
  for (const [sheetName, sheetPath] of sheetPathByName.entries()) {
    const sheetXml = await readZipText(zip, sheetPath);
    if (!sheetXml) continue;

    const cellMap = new Map<string, XlsxCellImage[]>();
    const addCellImage = (coord: string, img: XlsxCellImage) => {
      const list = cellMap.get(coord) || [];
      list.push(img);
      cellMap.set(coord, list);
    };

    // 5a. Scan worksheet <c> cells for vm="..." or DISPIMG("...")
    const cellRegex = /<(?:\w+:)?c\b([^>]*?)(?:\/>|>([\s\S]*?)<\/(?:\w+:)?c>)/gi;
    let cMatch: RegExpExecArray | null;
    while ((cMatch = cellRegex.exec(sheetXml)) !== null) {
      const attrs = cMatch[1];
      const inner = cMatch[2] || '';
      const rMatch = /\br\s*=\s*["']([A-Z]+\d+)["']/i.exec(attrs);
      if (!rMatch) continue;
      const cellCoord = rMatch[1].toUpperCase();

      const vmMatch = /\bvm\s*=\s*["'](\d+)["']/i.exec(attrs);
      if (vmMatch && vmToMedia.size > 0) {
        const vmIdx = parseInt(vmMatch[1], 10);
        const img = vmToMedia.get(vmIdx);
        if (img) {
          addCellImage(cellCoord, img);
          continue;
        }
      }

      if (dispImgToMedia.size > 0 && inner.includes('DISPIMG')) {
        const dispMatch = /DISPIMG\s*\(\s*(?:&quot;|["'])([^"'&]+)(?:&quot;|["'])/i.exec(inner);
        if (dispMatch) {
          const img = dispImgToMedia.get(dispMatch[1]);
          if (img) {
            addCellImage(cellCoord, img);
          }
        }
      }
    }

    // 5b. Scan worksheet drawings (xl/drawings/drawing*.xml)
    const sheetRelsXml = await readZipText(zip, relsPathFor(sheetPath));
    if (sheetRelsXml) {
      const sheetRels = parseRelsXml(sheetRelsXml, sheetPath);
      const drawingTagRegex = /<(?:\w+:)?drawing\b[^>]*?\b(?:\w+:)?id\s*=\s*["']([^"']+)["'][^>]*\/?>/gi;
      let dMatch: RegExpExecArray | null;
      while ((dMatch = drawingTagRegex.exec(sheetXml)) !== null) {
        const drawingPath = sheetRels.get(dMatch[1]);
        if (!drawingPath) continue;
        const drawingXml = await readZipText(zip, drawingPath);
        const drawingRelsXml = await readZipText(zip, relsPathFor(drawingPath));
        if (!drawingXml || !drawingRelsXml) continue;
        const drawingRels = parseRelsXml(drawingRelsXml, drawingPath);

        const anchorRegex = /<(?:\w+:)?(?:oneCellAnchor|twoCellAnchor)\b[^>]*>([\s\S]*?)<\/(?:\w+:)?(?:oneCellAnchor|twoCellAnchor)>/gi;
        let aMatch: RegExpExecArray | null;
        while ((aMatch = anchorRegex.exec(drawingXml)) !== null) {
          const anchorBody = aMatch[1];
          const fromMatch = /<(?:\w+:)?from\b[^>]*>([\s\S]*?)<\/(?:\w+:)?from>/i.exec(anchorBody);
          const blipMatch = /<(?:\w+:)?blip\b[^>]*?\b(?:\w+:)?embed\s*=\s*["']([^"']+)["']/i.exec(anchorBody);
          if (!fromMatch || !blipMatch) continue;
          const colMatch = /<(?:\w+:)?col\b[^>]*>(\d+)<\/(?:\w+:)?col>/i.exec(fromMatch[1]);
          const rowMatch = /<(?:\w+:)?row\b[^>]*>(\d+)<\/(?:\w+:)?row>/i.exec(fromMatch[1]);
          if (!colMatch || !rowMatch) continue;
          const col0 = parseInt(colMatch[1], 10);
          const row0 = parseInt(rowMatch[1], 10);
          const mediaPath = drawingRels.get(blipMatch[1]);
          const src = mediaPath ? mediaDataUrls.get(mediaPath) : undefined;
          if (!src) continue;
          const cNvPrMatch = /<(?:\w+:)?cNvPr\b([^>]*?)\/?>/i.exec(anchorBody);
          const descrMatch = cNvPrMatch ? /\b(?:descr|name)\s*=\s*["']([^"']+)["']/i.exec(cNvPrMatch[1]) : null;
          const cellCoord = `${colIndexToLetter(col0)}${row0 + 1}`;
          addCellImage(cellCoord, { src, alt: descrMatch ? descrMatch[1] : defaultCellImageAlt() });
        }
      }
    }

    if (cellMap.size > 0) {
      result.set(sheetName, cellMap);
    }
  }

  return result;
}

const PLACEHOLDER_CELL_TEXT_RE = /^(?:#VALUE!|#N\/A|#REF!|#NAME\?|#NULL!|=?(?:_xlfn\.)?(?:DISPIMG|IMAGE)\([\s\S]*\))$/i;

/**
 * Inject extracted cell images into the HTML table produced by
 * `XLSX.utils.sheet_to_html(sheet, { id: 'sheet-${sheetIdx}' })`.
 */
export function injectSheetHtmlImages(
  sheetHtml: string,
  sheetIdx: number,
  cellImages?: Map<string, XlsxCellImage[]>,
): string {
  if (!cellImages || cellImages.size === 0) return sheetHtml;

  return sheetHtml.replace(
    /<td\b([^>]*?\bid=["']sheet-(\d+)-([A-Z]+\d+)["'][^>]*)>([\s\S]*?)<\/td>/gi,
    (fullMatch, attrs: string, idxStr: string, coord: string, innerHtml: string) => {
      if (Number(idxStr) !== sheetIdx) return fullMatch;
      const images = cellImages.get(coord.toUpperCase());
      if (!images || images.length === 0) return fullMatch;

      const fallbackAlt = defaultCellImageAlt();
      const imgHtml = images
        .map(
          (img) =>
            `<img class="excel-cell-image" src="${escapeHtmlAttr(img.src)}" alt="${escapeHtmlAttr(img.alt || fallbackAlt)}" />`,
        )
        .join('');

      const trimmedText = innerHtml.replace(/<[^>]*>/g, '').trim();
      if (!trimmedText || PLACEHOLDER_CELL_TEXT_RE.test(trimmedText)) {
        return `<td${attrs}>${imgHtml}</td>`;
      }
      return `<td${attrs}>${imgHtml}<span>${innerHtml}</span></td>`;
    },
  );
}
