---
paths:
  - "apps/admin/**"
---

# Админка (`apps/admin`)

Спека — `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §9. Общие правила фронтов — `frontend.md`.

- **Структура:** `src/app/` — роутер, провайдеры, сессия, права, guard'ы; `src/ui/` — набор компонентов; `src/features/<раздел>/` — экраны; `src/api/` — клиент и мутации; `src/i18n/` — язык.
- **Компоненты** — только из `src/ui` (Task 12a/12b); своих кнопок, полей и модалок в `features` не заводить. До появления `src/ui` — временные заглушки `src/app/stubs.tsx`.
- **Права** — только из `me.permissions` (`useCan`, `useDecide` из `src/app/session.tsx`), не по роли: `allow` — сразу, `scoped` — в `me.city_ids`, `dual` — «можно запросить» (`useDecide` → `needs_approval`). Раздела без права нет в меню (`src/app/nav.ts`), прямой адрес — `ForbiddenState`. Решает всегда сервер.
- **Тексты** — только `messages/{ru,en}.json`, en — перевод, не пустая строка (D21). Подписи прав — `permissions.<ключ>`, действий подтверждений — `approvals.actions.<действие>`, ошибок — `errors.<код>`; стражи — `src/i18n/labels.test.ts`, `error-codes.test.ts`.
- **Пути** — только константы `src/app/paths.ts`; новый раздел — путь там, маршрут в `src/app/router.tsx`, пункт меню с правом в `nav.ts`.
- **Запросы** — `useApi()` (openapi-fetch, тот же origin, `credentials: 'same-origin'`). Клиент сам ставит `X-WF-Admin` на мутациях и бросает `ApiError {status, code, fields, retryAfter, attemptsLeft}` на любой не-2xx; `401 auth.reauth_required` — ждёт разблокировку (`reauthBus`) и повторяет запрос.
- **Мутации** — только `useAdminMutation(fn(vars, idempotencyKey))`: ключ идемпотентности — один на действие, `retry()` повторяет с тем же ключом; сам клиент ключ не создаёт (M6). `networkMode: 'always'` — офлайн не копится.
- **Фоновые опросы** (счётчики, статус импорта) — только с `...background()`: заголовок `X-WF-Background: 1` не продлевает сессию, и такой запрос не открывает модалку разблокировки сам.
- **Черновики форм** — `useDraft(<путь маршрута>)` (sessionStorage); пароли и коды в черновик не класть.
- **Токены** — только `--wf-*`; `text-faint` — только плейсхолдеры и неактивные иконки (контраст), подписи — `text-subtle`; `text-ghost` — только disabled; `breakpoint-mobile` — ширина переключения раскладки (в `@media` — через `tokens.ts`).
- **Тесты** — `renderWithProviders(ui, { me })` / `providers({ me })` из `src/test/render.tsx` (me в кэше; `me: null` — тест сам мокает `GET /v1/staff/me`), ответы `me` — `src/test/me.ts`, ошибки — `problem()` из `src/test/problem.ts`.
