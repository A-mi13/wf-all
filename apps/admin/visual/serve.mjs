// Статика для визуальных тестов (план UF3.6): node visual/serve.mjs <каталог> <порт>. Витрина (dist-stories) и
// админка режима shots (dist-shots) — одностраничные: путь без расширения → index.html. Только 127.0.0.1.
import { createReadStream, existsSync, statSync } from 'node:fs';
import { createServer } from 'node:http';
import { extname, join, normalize, resolve, sep } from 'node:path';

const [dirArg, portArg] = process.argv.slice(2);
if (!dirArg || !portArg) throw new Error('использование: node visual/serve.mjs <каталог> <порт>');
const root = resolve(dirArg);
if (!existsSync(join(root, 'index.html')))
  throw new Error(`${root}: нет index.html — сначала сборка`);

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.webmanifest': 'application/manifest+json',
};

const server = createServer((req, res) => {
  const path = decodeURIComponent(new URL(req.url ?? '/', 'http://x').pathname);
  let file = normalize(join(root, path));
  // с разделителем: иначе прошёл бы соседний каталог «<корень>-x»
  if (file !== root && !file.startsWith(root + sep)) {
    res.writeHead(403).end();
    return;
  }
  if (!extname(path) || !existsSync(file) || statSync(file).isDirectory()) {
    if (extname(path)) {
      res.writeHead(404).end();
      return;
    }
    file = join(root, 'index.html');
  }
  const headers = { 'Content-Type': TYPES[extname(file)] ?? 'application/octet-stream' };
  // воркер MSW режима shots управляет всем origin
  if (file.endsWith('mockServiceWorker.js')) headers['Service-Worker-Allowed'] = '/';
  res.writeHead(200, headers);
  createReadStream(file).pipe(res);
});
// порт 0 — свободный от ОС (тест); фактический адрес — строкой в stdout
server.listen(Number(portArg), '127.0.0.1', () => {
  const addr = server.address();
  console.log(`serve: http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : portArg}/`);
});
