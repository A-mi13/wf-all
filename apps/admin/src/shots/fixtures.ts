import type { components } from '../api/gen/admin';
import type { Me } from '../app/permissions';
import { SHOWCASE_NOW } from '../showcase/params';
import { approvalsServer } from '../test/approvals';
import { geoImport } from '../test/geo';
import { problem } from '../test/problem';
import { http, server } from '../test/server';
import { approval } from '../test/staff';

// Наборы данных режима shots (?shots-fixture=…, ревью UF4-H m3 и UF4-P m9): состояния, которых нет на моках
// sectionsServer по умолчанию. Вызывается до монтирования приложения; me — роль из адреса, набор может добавить ей
// право (главной с импортом нужно geo.imports.read). Неизвестное имя — ошибка (снимок не «молча без данных»).
// - home-busy — главная с делами: входящие и свои запросы, сбой и зависший импорт, резервных кодов 2; города в
//   ожидании — ошибка (плитка «Не загрузилось» и строка ошибки в списке дел);
// - palette-error — палитра: поиск городов отвечает 503 (ошибка одной группы, остальные живы);
// - imports-empty, imports-error — «Импорт GeoNames» (G9 Task 20): журнал пуст (иллюстрация и «Запустить импорт») или
//   отвечает 503 (ошибка с кодом и «Повторить»).
// «Ищем…» снимком не показать: скрипт снимков ждёт тишины сети (висящий запрос её не даёт) — состояние держат тесты.
export const SHOTS_FIXTURES = [
  'home-busy',
  'palette-error',
  'imports-empty',
  'imports-error',
] as const;
export type ShotsFixture = (typeof SHOTS_FIXTURES)[number];

const isFixture = (v: string): v is ShotsFixture =>
  (SHOTS_FIXTURES as readonly string[]).includes(v);

type Approval = components['schemas']['Approval'];
const MIN = 60_000;
const ago = (ms: number) => new Date(SHOWCASE_NOW.getTime() - ms).toISOString();
const rows = (n: number, prefix: string): Approval[] =>
  Array.from({ length: n }, (_, i) =>
    approval({ id: `0192f1e2-4c5d-7e6f-${prefix}-${String(i).padStart(12, '0')}` }),
  );

// Поиск палитры (q) — отдельно от остальных чтений городов (названия городов карточки — ids, список раздела).
const isPaletteSearch = (url: URL) =>
  url.searchParams.has('q') && url.searchParams.get('limit') === '5';

export function applyShotsFixture(name: string | null, me: Me | null): Me | null {
  if (name === null) return me;
  if (!isFixture(name)) throw new Error(`shots: неизвестный набор ${name}`);
  switch (name) {
    case 'home-busy':
      approvalsServer({ incoming: rows(3, 'a081'), mine: rows(2, 'b081'), pageSize: 100 });
      server.use(
        http.get('/v1/geo/imports', ({ response }) =>
          response(200).json({
            items: [
              geoImport({
                id: '0192f1f5-1a2b-7c3d-8e4f-000000000001',
                country_code: 'RU',
                status: 'failed',
                started_at: ago(130 * MIN),
                finished_at: ago(120 * MIN),
              }),
              geoImport({
                id: '0192f1f5-1a2b-7c3d-8e4f-000000000003',
                country_code: 'KZ',
                status: 'running',
                started_at: ago(40 * MIN),
                finished_at: null,
              }),
            ],
            next_cursor: null,
          }),
        ),
        http.get('/v1/staff/me/mfa', ({ response }) =>
          response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 2 }),
        ),
        http.get('/v1/geo/cities', ({ request, response }) =>
          new URL(request.url).searchParams.getAll('status').includes('waitlist')
            ? response.untyped(problem(500, 'internal'))
            : undefined,
        ),
      );
      return me && { ...me, permissions: { ...me.permissions, 'geo.imports.read': 'allow' } };
    case 'palette-error':
      server.use(
        http.get('/v1/geo/cities', ({ request, response }) =>
          isPaletteSearch(new URL(request.url))
            ? response.untyped(problem(503, 'internal'))
            : undefined,
        ),
      );
      return me;
    case 'imports-empty':
      server.use(
        http.get('/v1/geo/imports', ({ response }) =>
          response(200).json({ items: [], next_cursor: null }),
        ),
      );
      return me;
    case 'imports-error':
      server.use(
        http.get('/v1/geo/imports', ({ response }) => response.untyped(problem(503, 'internal'))),
      );
      return me;
  }
}
