import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import type { Plugin } from 'vite';

// Плагин режима shots (спека §5.4, план UF1a.1, U3) — включает его vite.config.ts только при
// `--mode shots`:
// (1) resolveId: src/test/server.ts и src/test/base.ts → src/shots/server.ts и src/shots/base.ts.
//     Фикстуры импортируют их относительно (`./server`, `../test/server`) — алиас по спецификатору их
//     не ловит, поэтому сравнивается разрешённый абсолютный путь (Windows: `\` → `/`, регистр диска);
// (2) configureServer: /mockServiceWorker.js — из пакета msw (allowBuilds: msw: false — postinstall
//     воркер не копирует), а не из public/: оттуда admin-api отдал бы его в проде;
// (3) transformIndexHtml: тот же index.html, точка входа /src/main.tsx → /src/shots/main.tsx;
// (4) generateBundle: при `vite build --mode shots` (экраны визуальных тестов CI) воркер — файлом в выход
//     (dist-shots — vite.config.ts; не dist: его отдаёт прод-проект e2e и проверяет check-admin-dist.mjs).

const WORKER = '/mockServiceWorker.js';
const ENTRY = '/src/main.tsx';
const SHOTS_ENTRY = '/src/shots/main.tsx';

// Путь для сравнения: прямые слэши, без расширения .ts, без регистра (диск Windows, NTFS).
export function samePath(a: string, b: string): boolean {
  const norm = (p: string) =>
    p
      .replace(/[?#].*$/, '')
      .replaceAll('\\', '/')
      .replace(/\.ts$/, '')
      .toLowerCase();
  return norm(a) === norm(b);
}

export function wfShots(root: string = import.meta.dirname): Plugin {
  const swap: [from: string, to: string][] = [
    [join(root, 'src/test/server.ts'), join(root, 'src/shots/server.ts')],
    [join(root, 'src/test/base.ts'), join(root, 'src/shots/base.ts')],
  ];
  const worker = createRequire(join(root, 'package.json')).resolve('msw/mockServiceWorker.js');
  return {
    name: 'wf-shots',
    enforce: 'pre',
    async resolveId(source, importer, options) {
      // дёшево отсечь всё, что не может быть server/base
      if (!/(^|[\\/])(server|base)(\.ts)?$/.test(source)) return null;
      const resolved = await this.resolve(source, importer, { ...options, skipSelf: true });
      if (!resolved) return null;
      const hit = swap.find(([from]) => samePath(resolved.id, from));
      // прямые слэши — как id самого Vite: иначе на Windows сборка видит два модуля одного файла (два воркера)
      return hit ? hit[1].replaceAll('\\', '/') : null;
    },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (new URL(req.url ?? '/', 'http://x').pathname !== WORKER) return next();
        res.writeHead(200, {
          'Content-Type': 'application/javascript; charset=utf-8',
          'Cache-Control': 'no-cache',
          'Service-Worker-Allowed': '/',
        });
        res.end(readFileSync(worker, 'utf8'));
      });
    },
    // сборка режима shots (экраны визуальных тестов CI, UF3.6): воркер — в выход dist-shots, как его отдаёт dev
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: WORKER.slice(1),
        source: readFileSync(worker, 'utf8'),
      });
    },
    // order 'pre': при сборке хук без порядка зовётся и после бандла — по HTML, где точка входа уже заменена
    // собранным файлом (и падал бы на «нет точки входа»)
    transformIndexHtml: {
      order: 'pre',
      handler(html) {
        if (!html.includes(`src="${ENTRY}"`)) {
          throw new Error(`wf-shots: в index.html нет точки входа ${ENTRY}`);
        }
        return html.replace(`src="${ENTRY}"`, `src="${SHOTS_ENTRY}"`);
      },
    },
  };
}
