---
paths:
  - "apps/admin/src/features/captains/**"
  - "apps/admin/src/features/players/**"
  - "apps/admin/src/features/staff/PlayerLinkBlock*"
  - "apps/admin/src/features/flags/EmergencyDialog*"
  - "apps/admin/src/features/flags/authFlags*"
  - "apps/admin/src/test/identity*"
---

# Админка: разделы identity («Капитаны», «Игроки», связь сотрудника, флаги входа)

Ядро — `admin.md`; спека identity §7.4–7.5, план I-4 (Task 8, 9; R19–R22, R27, R28, R41).

## Разделы
- Группа меню `nav.groups.players` после «Обзора»: «Капитаны» (`/captains`, `identity.captain_applications.read`,
  значок `captain`, `g k`, счётчик) и «Игроки» (`/players`, `identity.pii.read`, значок `team`, `g u`). На телефоне
  открыты целиком: решения по заявкам, запрос и отмена сброса 2FA, связь сотрудника — не выдача прав (R27), реестр
  `DESKTOP_ONLY_PERMISSIONS` не меняется. Модератору сервер сужает до своих городов (чужое — 404, как несуществующее).
- «Капитаны» — очередь (`DataTable`, фильтры `status|city` в адресе), карточка — панель `?application=<id>`.
  «Одобрить» недоступна, пока хоть одно условие не `ok` (незнакомое состояние тоже блокирует) — R22, сервер
  перепроверяет. Свободный текст игрока (`note`, комментарии) — `pre-wrap`, без разметки.
- Счётчик «Капитаны» — `useCaptainQueueCount()` (`['identity','captains','count']`, `status=pending&limit=100`,
  `...background()`, 60 с), плитка главной — та же подписка. `defaultHandlers` (`src/test/defaults.ts`) отвечает
  пустой очередью: счётчик опрашивает на любом разделе.

## ПД — только в памяти экрана (R19, R41)
- Контакты заявителя, поиск игрока (страница «Игроки» и окно связи сотрудника) — только с причиной: заголовок
  `X-WF-Access-Reason` (и почта — `X-WF-Lookup-Email`) в `encodeURIComponent`. Причина — `captains/reason.ts`:
  3–200 символов (кодовые точки), безопасные невидимые (U+00AD, U+200B–U+200F, U+FEFF) срезаются, прочие Cc/Cf/Zl/Zp
  — ошибка поля; поле однострочное, подсказка «не указывайте персональные данные игрока» (причина в журнале
  неизменяема).
- Каждое чтение — запись в журнале: запрос только по явной отправке, `staleTime: Infinity`, `gcTime: 0`,
  `retry: false`, `refetchOnWindowFocus/Reconnect: false`, `meta: ONE_SHOT`; ключ — без почты и причины (контакты —
  `['identity','captain-contact',id]` вне `CAPTAINS_KEY`; поиск — `usePlayerLookup(scope)`, ключ
  `['identity','player-lookup',scope,n]`, n — номер отправки), повторная отправка, пока идёт чтение, гасится.
- Почта, причина и результат — не в адресе, storage, черновиках (`useDraft`), «Недавнем» палитры и кэше дольше
  экрана; уход со страницы и закрытие окна стирают. Тесты — `storageDump()`, `app.at()`, `fireEvent.focus(window)`
  и `online` (счётчик запросов мока = 1).
- После действия (отмена сброса 2FA) карточка обновляется по ответу (`setQueryData`), а не повторным поиском: он
  потребовал бы снова причину и дал бы лишнюю запись `pii_read`.

## Сброс 2FA и связь сотрудника
- «Запросить сброс 2FA» — право `identity.mfa.reset` (`dual` — тоже разрешение), только при включённой 2FA и без
  назначенного сброса; всегда `202` → `PendingApprovalNotice`, инвалидация `['approvals']`. «Отменить сброс» —
  `ConfirmDialog` с ником → `200` (не `202`); `404 mfa_reset_not_found` — тост и сброс снят из карточки.
- Связь «сотрудник → учётка игрока» — блок `PlayerLinkBlock` в панели сотрудника, только при
  `staff.player_links.manage`; поиск — тот же `usePlayerLookup`/`LookupForm`, что в «Игроках»; ошибки PUT — в окне;
  ключ мутаций — `ACCOUNT_ACTION_KEY` (панель не закрывается, пока запрос идёт), после ответа — `['staff','accounts']`.
- Сводка подтверждения `identity.mfa_reset` (`summarizePayload(payload, action)`): «Игрок <user_id>», обращение,
  причина; незнакомые поля — «и ещё N полей»; карточку сотрудника по `target_id` не запрашивает (это игрок).
- Фокус после действия, убравшего свою кнопку (решение, показ контактов, сброс, связь), — на панель или карточку
  (`tabIndex -1`), если поверх нет окна; не на body.

## Флаги способов входа (R3)
- Правила — только из `FeatureFlag.rules` сервера: `global_only` — строка без городских клеток (на телефоне — без
  «Города»), `requires` — «Требует «…»» и обратное «Нужен для «…»» по `requires` других флагов.
- Выключение аварийного флага входа (`LOGIN_FLAG_METHOD` в `flags/authFlags.ts`, страж ключей —
  `authFlags.test.ts`) — `EmergencyDialog` на любом устройстве: число `only_method` из `getAuthMethodsUsage`; ошибка
  статистики — «Повторить» без числа, «Выключить» доступно (авария важнее). Включение — без окна.

## Тесты и моки
- `identityServer()` (`src/test/identity.ts`): очередь, карточка, контакты, решения, поиск (`players` по почте,
  иначе 404), сброс и отмена (`resetAnswer`/`cancelAnswer`), статистика (`usage`); `seen` — что спрашивал экран,
  ворота `holdCard`/`holdContact`/`holdLookup`. Связь сотрудника — `staffServer({ nicknames, linkAnswer })`.
- Снимки — `?shots-fixture=captains-busy` и `identity-support` (`src/shots/fixtures.ts`); карточку найденного
  игрока снимком не показать (поиск — по вводу), её держат тесты.
