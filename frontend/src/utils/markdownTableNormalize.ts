/** Matches a GFM alignment cell (---, :---, ---:, :---:). */
const SEPARATOR_CELL = /^:?-{3,}:?$/;

function splitRowCells(line: string): string[] {
  const inner = line.trim();
  if (!inner.startsWith('|')) {
    return [];
  }
  // GFM treats a pipe preceded by an odd backslash run as cell text.
  // Keep that spelling intact; only count actual column delimiters when
  // reconstructing the alignment row for imported tables.
  let parts: string[] = [];
  let start = 0;
  let backslashes = 0;
  for (let i = 0; i < inner.length; i += 1) {
    const char = inner[i];
    if (char === '|' && backslashes % 2 === 0) {
      parts.push(inner.slice(start, i));
      start = i + 1;
    }
    backslashes = char === '\\' ? backslashes + 1 : 0;
  }
  parts.push(inner.slice(start));
  if (parts.length && parts[0].trim() === '') {
    parts = parts.slice(1);
  }
  if (parts.length && parts[parts.length - 1].trim() === '') {
    parts = parts.slice(0, -1);
  }
  return parts.map((part) => part.trim());
}

function isTableRow(line: string): boolean {
  const stripped = line.trim();
  return stripped.startsWith('|') && stripped.includes('|', 1);
}

function isSeparatorRow(line: string): boolean {
  const cells = splitRowCells(line);
  return cells.length > 0 && cells.every((cell) => SEPARATOR_CELL.test(cell));
}

function isEmptyRow(line: string): boolean {
  const cells = splitRowCells(line);
  return cells.length > 0 && cells.every((cell) => cell === '');
}

function separatorRowFor(headerLine: string): string {
  const cells = splitRowCells(headerLine);
  const eol = headerLine.endsWith('\r') ? '\r' : '';
  return `| ${cells.map(() => '---').join(' | ')} |${eol}`;
}

function normalizeTableBlock(block: string[]): string[] {
  let rows = [...block];
  while (rows.length && isEmptyRow(rows[0])) {
    rows.shift();
  }
  if (rows.length && isSeparatorRow(rows[0])) {
    rows.shift();
  }
  if (rows.length >= 2 && !isSeparatorRow(rows[1])) {
    rows = [rows[0], separatorRowFor(rows[0]), ...rows.slice(1)];
  }
  return rows;
}

const FENCE_LINE_RE = /^ {0,3}(`{3,}|~{3,})([^\r\n]*)\r?$/;

/** Fix MarkItDown-style tables: empty row + separator before real rows, skipping fenced code blocks. */
export function normalizeSpuriousTablePrefixes(content: string): string {
  const lines = content.split('\n');
  const out: string[] = [];
  let openFence: { char: string; length: number } | null = null;
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const fenceMatch = FENCE_LINE_RE.exec(line);
    if (openFence) {
      if (
        fenceMatch &&
        fenceMatch[1][0] === openFence.char &&
        fenceMatch[1].length >= openFence.length &&
        /^[ \t]*$/.test(fenceMatch[2])
      ) {
        openFence = null;
      }
      out.push(line);
      i += 1;
      continue;
    }
    if (fenceMatch && (fenceMatch[1][0] !== '`' || !fenceMatch[2].includes('`'))) {
      openFence = { char: fenceMatch[1][0], length: fenceMatch[1].length };
      out.push(line);
      i += 1;
      continue;
    }
    if (!isTableRow(line)) {
      out.push(line);
      i += 1;
      continue;
    }
    const block: string[] = [];
    while (i < lines.length && !FENCE_LINE_RE.test(lines[i]) && isTableRow(lines[i])) {
      block.push(lines[i]);
      i += 1;
    }
    out.push(...normalizeTableBlock(block));
  }
  return out.join('\n');
}

