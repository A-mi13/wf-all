import { auditServer } from './audit';
import { geoServer } from './geo';
import { http, server } from './server';
import { staffServer } from './staff';

// Первые запросы разделов каркаса, куда приводит роутер (стартовая страница, возврат по ?return=, вход
// после выхода): журнал (пусто), «Роли и доступ» (матрица и одна учётка), «Мой доступ» (второй фактор,
// сеансы), «Справочники» (чтения по умолчанию — src/test/geo.ts), главная (src/features/home: входящие и свои —
// defaultHandlers, второй фактор — ниже, импорт и города в ожидании — geoServer). Для тестов маршрутов и входа:
// содержимое раздела им не важно, но неописанный запрос роняет тест (src/test/setup.ts). Тест раздела
// ставит свои ответы (server.use позже — важнее).
export function sectionsServer() {
  auditServer({ records: [] });
  const geo = geoServer();
  staffServer();
  server.use(
    // Список городов с фильтром статуса (главная: «Города в ожидании») — по городам справочника geoServer: мок
    // «Ролей и доступа» (staffServer, поставлен позже — важнее) статус не разбирает. Без status — дальше, к нему.
    http.get('/v1/geo/cities', ({ request, response }) => {
      const statuses = new URL(request.url).searchParams.getAll('status');
      if (statuses.length === 0) return undefined;
      const items = geo.state.cities.filter((c) => statuses.includes(c.status));
      return response(200).json({ items, next_cursor: null });
    }),
    http.get('/v1/staff/me/mfa', ({ response }) =>
      response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 10 }),
    ),
    http.get('/v1/staff/me/sessions', ({ response }) => response(200).json({ items: [] })),
  );
}
