// Run after QAG_RENDER_FIXTURE_DIR=<dir> go test -count=1 ./internal/reporting.
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
import { getDocument, OPS } from '../web/node_modules/pdfjs-dist/legacy/build/pdf.mjs';

const directory = process.env.QAG_RENDER_FIXTURE_DIR || '.local/verify/mcp-render';
const loading = getDocument({ data: new Uint8Array(await readFile(resolve(directory, 'qag-report.pdf'))), useSystemFonts: false });
const pdf = await loading.promise;
let text = '', images = 0, tablePages = 0;
for (let number = 1; number <= pdf.numPages; number++) {
  const page = await pdf.getPage(number);
  const content = await page.getTextContent();
  const pageText = content.items.map(item => item.str || '').join('');
  text += pageText;
  if (pageText.includes('numeric')) tablePages++;
  const operators = await page.getOperatorList();
  images += operators.fnArray.filter(op => op === OPS.paintImageXObject || op === OPS.paintInlineImageXObject).length;
}
assert(pdf.numPages >= 3, 'expected pagination');
assert(text.includes('Проверка кириллицы'), 'Cyrillic text lost');
assert(text.includes('JSON-END') && text.includes('TABLE-END'), 'content silently cropped');
assert.equal((text.match(/9007199254740993123\.125/g) || []).length, 200, 'numeric precision/content lost');
assert(text.includes('NULL') && text.includes('""'), 'SQL null/empty distinction lost');
assert(tablePages >= 2, 'table headings not repeated');
assert(images >= 1, 'reference image missing');
console.log(JSON.stringify({ pages: pdf.numPages, tablePages, images, exactNumbers: 200, cyrillic: true }));
await loading.destroy();
