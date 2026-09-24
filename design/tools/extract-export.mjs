// Достаёт исходники экранов из HTML-экспорта Design-канваса.
// Бандл: манифест {uuid: {mime, compressed, data}} + шаблон с iframe на каждый артборд;
// каждая страница — сама бандл, её шаблон содержит <x-dc>…</x-dc> и логику экрана.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gunzipSync } from 'node:zlib';

// Берём ПОСЛЕДНЕЕ вхождение: та же строка встречается в JS загрузчика бандла.
// required=true — бросить понятную ошибку, если блока нет (иначе вызывающий сам решает).
export function readBlock(html, type, required = false) {
  const open = `<script type="__bundler/${type}">`;
  const i = html.lastIndexOf(open);
  if (i < 0) {
    if (required) throw new Error(`не найден блок __bundler/${type} — это не HTML-экспорт дизайн-канваса?`);
    return null;
  }
  const start = i + open.length;
  return html.slice(start, html.indexOf('</script>', start));
}

// @font-face ссылаются на uuid-ресурсы бандла и вне него не работают. Шрифты — Oswald и Manrope.
export const stripFonts = (src) => src.replace(/<style>[^<]*@font-face[\s\S]*?<\/style>/g, '');

export const fileName = (index, title) =>
  `${String(index + 1).padStart(2, '0')}-${title.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '')}.dc.html`;

function decode(entry) {
  const raw = Buffer.from(entry.data, 'base64');
  return (entry.compressed ? gunzipSync(raw) : raw).toString('utf8');
}

export function extractBoards(html) {
  const manifest = JSON.parse(readBlock(html, 'manifest', true));
  const template = JSON.parse(readBlock(html, 'template', true));
  const re = /about:blank#([0-9a-f-]{36})" title="([^"]*)" width="(\d+)" height="(\d+)"/g;
  return [...template.matchAll(re)].map(([, id, title, width, height]) => {
    const inner = JSON.parse(readBlock(decode(manifest[id]), 'template', true));
    const markupStart = inner.indexOf('<x-dc>');
    const markupEnd = inner.lastIndexOf('</x-dc>');
    if (markupStart < 0 || markupEnd < 0) throw new Error(`у артборда «${title}» не найден <x-dc> — повреждён экспорт?`);
    const markup = inner.slice(markupStart, markupEnd + '</x-dc>'.length);
    const logic = inner.match(/<script type="text\/x-dc"[\s\S]*?<\/script>/)?.[0] ?? '';
    return { title, width: Number(width), height: Number(height), source: `${stripFonts(markup)}\n\n${logic}\n` };
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [bundlePath, outDir] = process.argv.slice(2);
  if (!bundlePath || !outDir) {
    console.error('usage: node design/tools/extract-export.mjs <бандл.html> <папка>');
    process.exit(1);
  }
  const boards = extractBoards(await readFile(bundlePath, 'utf8'));
  await mkdir(outDir, { recursive: true });
  for (const [i, b] of boards.entries()) await writeFile(join(outDir, fileName(i, b.title)), b.source);
  console.log(`извлечено артбордов: ${boards.length}`);
}
