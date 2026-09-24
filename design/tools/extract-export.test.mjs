// design/tools/extract-export.test.mjs
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { gzipSync } from 'node:zlib';
import { extractBoards, fileName } from './extract-export.mjs';

// JSON внутри <script> в настоящем бандле экранирует закрывающий тег — повторяем это.
const island = (type, value) =>
  `<script type="__bundler/${type}">${JSON.stringify(value).replaceAll('</script>', '<\\/script>')}</script>`;

const pageHtml = island(
  'template',
  '<html><style>@font-face{font-family:X;src:url("u")}</style><x-dc><div>Привет</div></x-dc>' +
    '<script type="text/x-dc" data-dc-script="">class Component {}</script></html>',
);
const id = '11111111-2222-3333-4444-555555555555';
const bundle =
  island('manifest', {
    [id]: { mime: 'text/html', compressed: true, data: gzipSync(pageHtml).toString('base64') },
  }) +
  island(
    'template',
    `<iframe src="about:blank#${id}" title="Лента города" width="390" height="844"></iframe>`,
  );

test('достаёт артборд: разметка и логика без шрифтов', () => {
  const [board] = extractBoards(bundle);
  assert.equal(board.title, 'Лента города');
  assert.equal(board.width, 390);
  assert.match(board.source, /<x-dc><div>Привет<\/div><\/x-dc>/);
  assert.match(board.source, /class Component/);
  assert.doesNotMatch(board.source, /@font-face/);
});

test('имя файла — номер и название', () => {
  assert.equal(fileName(2, 'Лента города'), '03-Лента-города.dc.html');
});
