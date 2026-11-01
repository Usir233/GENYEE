import assert from 'node:assert/strict';
import test from 'node:test';
import JSZip from 'jszip';
import { extractXlsxCellImages, injectSheetHtmlImages } from './xlsxPreviewImages';

const TINY_PNG_B64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';

async function buildRichDataXlsx(): Promise<ArrayBuffer> {
  const zip = new JSZip();
  zip.file(
    'xl/workbook.xml',
    '<?xml version="1.0"?><workbook><sheets><sheet name="采购供应商明细" sheetId="1" r:id="rId1"/></sheets></workbook>',
  );
  zip.file(
    'xl/_rels/workbook.xml.rels',
    '<?xml version="1.0"?><Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>',
  );
  zip.file(
    'xl/worksheets/sheet1.xml',
    '<?xml version="1.0"?><worksheet><sheetData><row r="2"><c r="A2" t="e" vm="1"><v>#VALUE!</v></c></row></sheetData></worksheet>',
  );
  zip.file(
    'xl/metadata.xml',
    '<?xml version="1.0"?><metadata><valueMetadata count="1"><bk><rc t="1" v="0"/></bk></valueMetadata></metadata>',
  );
  zip.file(
    'xl/richData/rdrichvalue.xml',
    '<?xml version="1.0"?><rvData count="1"><rv s="0"><v>0</v><v>5</v><v>产品实拍图</v></rv></rvData>',
  );
  zip.file(
    'xl/richData/richValueRel.xml',
    '<?xml version="1.0"?><richValueRels><rel r:id="rId1"/></richValueRels>',
  );
  zip.file(
    'xl/richData/_rels/richValueRel.xml.rels',
    '<?xml version="1.0"?><Relationships><Relationship Id="rId1" Target="../media/image1.png"/></Relationships>',
  );
  zip.file('xl/media/image1.png', TINY_PNG_B64, { base64: true });
  return zip.generateAsync({ type: 'arraybuffer' });
}

test('extractXlsxCellImages and injectSheetHtmlImages replace #VALUE! with inline <img>', async () => {
  const buf = await buildRichDataXlsx();
  const sheetMap = await extractXlsxCellImages(buf);
  assert.equal(sheetMap.has('采购供应商明细'), true);
  const cellMap = sheetMap.get('采购供应商明细')!;
  assert.equal(cellMap.get('A2')?.[0]?.alt, '产品实拍图');
  assert.equal(cellMap.get('A2')?.[0]?.src.startsWith('data:image/png;base64,'), true);

  const rawHtml =
    '<table id="sheet-0"><tr><td data-t="e" id="sheet-0-A2">#VALUE!</td><td id="sheet-0-B2">随行储能</td></tr></table>';
  const injected = injectSheetHtmlImages(rawHtml, 0, cellMap);
  assert.equal(injected.includes('#VALUE!'), false);
  assert.equal(injected.includes('class="excel-cell-image"'), true);
  assert.equal(injected.includes('alt="产品实拍图"'), true);
});
