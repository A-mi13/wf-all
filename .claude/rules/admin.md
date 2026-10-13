---
paths:
  - "apps/admin/**"
---

# Админка (`apps/admin`)

Спека — `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §9. Общие правила фронтов — `frontend.md`.

- **Структура:** `src/app/` — роутер, провайдеры, сессия, права, guard'ы; `src/ui/` — набор компонентов; `src/features/<раздел>/` — экраны; `src/api/` — клиент и мутации; `src/i18n/` — язык.
- **Компоненты** — только из `src/ui` (импорт `../../ui`); своих кнопок, полей и модалок в `features` не заводить. Каркас, таблица и состояния — Task 12b, до них — заглушки `src/app/stubs.tsx`.
  - Примитивы: `Button` (`variant`: primary | secondary | danger | danger-outline | link; `size="lg"` — вход; `loading`; по умолчанию `type="button"`), `StatusChip` (`tone`, текст обязателен), `CountBadge` (0 не рисуется, >99 — «99+»), `Avatar` (инициалы, декоративный).
  - Формы — `Form form={useForm()} onSubmit={(v) => m.mutateAsync(v)}` и поля `TextField`, `PasswordField`, `SelectField`, `MultiSelectField` (`searchable`, `onSearch` — поиск на сервере), `CheckboxField`, своё поле через `Controller` + `FieldError`. Ошибка `onSubmit`: `validation.failed` → `body.<поле>` раскладывается по полям (текст `ui.field_errors.<код>`), остальное — `FormError` (`errors.<код>`, откат на `errors.internal`). Особые случаи (`attempts_left`, 429) — `onError` экрана, вернуть `true`. Текст ошибки по коду вне формы — только `useErrorText()` или `<FormError error={…} />`, своих откатов не писать.
  - `OtpInput` — код второго фактора: `onComplete(code)` один раз на полный ввод, после ошибки — `ref.current.reset()` (очистка и фокус); резервный код — обычный `TextField`.
  - Оверлеи `Modal`, `Drawer` (панель справа), `Sheet` (шторка снизу), `ConfirmDialog` (`confirmWord` — слово или почта, точное совпадение): `open` + `onClose` у экрана, фокус-ловушка, Esc (только верхний), возврат фокуса на открыватель, блок прокрутки — внутри. `dismissible={false}` / `loading` — пока идёт запрос. `ConfirmDialog` на узком экране сам становится шторкой (`useIsMobile`, порог — токен `breakpoint-mobile`).
  - Тосты — `useToast().success|info|error(текст, { action })`: успех и справка исчезают через 8 с (`TOAST_HIDE_MS`, пауза при наведении и фокусе), ошибка — только руками. `ToastProvider` стоит в `AppProviders`.
  - CSP admin-api (`style-src 'self'`): в `src/ui` нет атрибута `style`, `<style>` и записи в `element.style` — только CSS Modules; цвета — только `var(--wf-color-*)`. Страж — `src/ui/csp.test.ts`.
- **Права** — только из `me.permissions` (`useCan`, `useDecide` из `src/app/session.tsx`), не по роли: `allow` — сразу, `scoped` — в `me.city_ids`, `dual` — «можно запросить» (`useDecide` → `needs_approval`). Раздела без права нет в меню (`src/app/nav.ts`), прямой адрес — `ForbiddenState`. Решает всегда сервер.
- **Тексты** — только `messages/{ru,en}.json`, en — перевод, не пустая строка (D21). Подписи прав — `permissions.<ключ>`, действий подтверждений — `approvals.actions.<действие>`, ошибок — `errors.<код>`; стражи — `src/i18n/labels.test.ts`, `error-codes.test.ts`.
- **Пути** — только константы `src/app/paths.ts`; новый раздел — путь там, маршрут в `src/app/router.tsx`, пункт меню с правом в `nav.ts`.
- **Запросы** — `useApi()` (openapi-fetch, тот же origin, `credentials: 'same-origin'`). Клиент сам ставит `X-WF-Admin` на мутациях и бросает `ApiError {status, code, fields, retryAfter, attemptsLeft}` на любой не-2xx; `401 auth.reauth_required` — ждёт разблокировку (`reauthBus`) и повторяет запрос.
- **Мутации** — только `useAdminMutation(fn(vars, idempotencyKey))`: ключ идемпотентности — один на действие, `retry()` повторяет с тем же ключом; сам клиент ключ не создаёт (M6). `networkMode: 'always'` — офлайн не копится.
- **Фоновые опросы** (счётчики, статус импорта) — только с `...background()` (возвращает лишь `{ headers }`): заголовок `X-WF-Background: 1` не продлевает сессию. `401 auth.reauth_required` фонового запроса, как и любого, открывает разблокировку (§9.3: через 20 минут бездействия вкладка показывает модалку, а не замёрзший счётчик); открыта уже — запрос ждёт её же, второй раз не открывает.
- **Черновики форм** — `useDraft(<путь маршрута>)` (sessionStorage); пароли и коды в черновик не класть.
- **Токены** — только `--wf-*`; `text-faint` — только плейсхолдеры и неактивные иконки (контраст), подписи — `text-subtle`; `text-ghost` — только disabled; `breakpoint-mobile` — ширина переключения раскладки (в `@media` — через `tokens.ts`).
- **Тесты** — `renderWithProviders(ui, { me })` / `providers({ me })` из `src/test/render.tsx` (me в кэше; `me: null` — тест сам мокает `GET /v1/staff/me`), ответы `me` — `src/test/me.ts`, ошибки — `problem()` из `src/test/problem.ts`. Компонент `src/ui` без API и сессии — `renderWithIntl(ui, { locale })` (язык и тосты, возвращает `user`); с `vi.useFakeTimers()` события — `fireEvent`: user-event зависает (RTL ждёт настоящий `setTimeout`).
