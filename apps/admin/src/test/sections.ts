import { auditServer } from './audit';
import { http, server } from './server';
import { staffServer } from './staff';

// Первые запросы разделов каркаса, куда приводит роутер (стартовая страница, возврат по ?return=, вход
// после выхода): журнал (пусто), «Роли и доступ» (матрица и одна учётка), «Мой доступ» (второй фактор,
// сеансы). Для тестов маршрутов и входа: содержимое раздела им не важно, но неописанный запрос роняет тест
// (src/test/setup.ts). Тест раздела ставит свои ответы (server.use позже — важнее).
export function sectionsServer() {
  auditServer({ records: [] });
  staffServer();
  server.use(
    http.get('/v1/staff/me/mfa', ({ response }) =>
      response(200).json({ confirmed_at: '2026-10-01T09:12:00Z', recovery_codes_left: 10 }),
    ),
    http.get('/v1/staff/me/sessions', ({ response }) => response(200).json({ items: [] })),
  );
}
