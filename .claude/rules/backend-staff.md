---
paths:
  - "backend/internal/staff/**"
---

# Модуль staff (сотрудники админки)

Спека — `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §4; решения плана A —
`.superpowers/sdd/2026-10-06-staff-a/decisions-r2.md` (D7–D12) и приложение Б плана (P1–P11).

- **Пакеты без циклов (P1–P5).** Корень `staff` — только `Deps`, `Meta`, DTO, ошибки (`Err*`, `MFAError`,
  `RateLimitError`, `CodeOf`), коды (`codes.go`, сверка с контрактом — `TestStaffCodesDocumented` в `admin`),
  события (`events.go`) и задача письма (`MailArgs`, `NewMailArgs`, очередь `queue.Staff`); корень не
  импортирует `internal/app`, `admin`, `jobs` (страж `TestRootDoesNotImportInnerPackages`). SQL — только
  `staff_*` (`queries/` → `internal/store/staffdb`, генерация — `go tool -modfile=tools/go.mod sqlc generate`).
- **Права** — `perm.Grants(role)` при загрузке сессии (`LoadStaff`); модуль своих матриц не держит.
- **Блокировки строк — всегда в порядке учётка → сессия → `staff_mfa` → резервные коды** (`LockAccount`,
  `LockSession`, `LockMFA`, `LockUnusedRecoveryCodes`). Иначе блокировка (гасит все сессии под замком учётки) и
  разблокировка в другой вкладке взаимно ждут.
- **Неудача второго фактора (D8):** фиксируется и коммитится до ответа 4xx, бизнес-ошибка её не откатывает;
  под `Idempotency-Key` — ровно одна закоммиченная транзакция. Любая проверка фактора (вход, unlock, смена
  пароля; 7 — перевыпуск кодов) — только через `a.withFactor(ctx, s, шаг, m, check)`: одна транзакция на ctx без
  отмены (`detached`), замок учётки, `check` → `(fail, err)`, где `fail` — вид неудачи (`ErrMFAInvalid` |
  `ErrPasswordInvalid`, nil — успех; иной `fail` — ошибка без записи неудачи), и при неудаче `recordFailure` в
  той же транзакции под тем же замком (счётчик, аудит, на пятой подряд — `lockOut`: `locked_until` +1 ч, сессии
  `locked`, событие `staff.locked`, письмо владельцам и админам не чаще раза в час по `lock_notified_at`).
  Отдельной транзакцией после отката неудачу не писать: параллельные попытки обходят порог «5 подряд»
  (стражи `TestConcurrentFailuresLockOnce`, `TestChangePasswordConcurrentFailures`). `check` первым шагом зовёт
  `lockLiveSession` (сессия чужая, погашена, истекла, учётка закрыта — `auth.ErrSessionInvalid`, заменена —
  `errReplaced`; отказ — без записи неудачи), стадию проверяет сам; `withFactor` этого не навязывает (путь
  `TestNoRelockWhileLocked`). На уже закрытой учётке `lockOut` не повторяется. Счётчик обнуляет только успех второго
  фактора: после истечения блокировки каждая неудача снова закрывает учётку. Неудача пароля учётку не трогает
  (D7): лимит по почте `ratelimit.EmailKey("staff_login", HMAC)` (10 за 15 мин, каждая попытка) и IP-лимит
  конвейера; закрытая учётка и несуществующая почта — `VerifyDummy` без `Verify` (страж
  `TestLoginTimingEqualized`, шов `passwordHasher`); аудит неудачи пароля — тоже на `detached`.
- **Смена токена (D9) — только `a.replaceSession`:** старая строка гасится как `replaced`, новая — с новым
  токеном (`token.NewRefresh`, в базе SHA-256), текущим IP и User-Agent. Старый токен минуту (`replacedGrace`)
  загрузчик отвечает `auth.ErrSessionReplaced` — конвейер не стирает cookie (гонка параллельного запроса), потом
  `ErrSessionInvalid`. Вторая вкладка с тем же кодом (сессия уже `replaced`) — `staff.mfa_invalid` без записи
  неудачи. Unlock не продлевает абсолютный срок (`created_at`, `expires_at` — от входа).
- **Секреты:** секрет TOTP — `sealTOTP`/`openTOTP` (`keys.Ring`, aad = id сотрудника); резервный код —
  `a.recoveryMAC(key_id строки, staffID, domain.NormalizeRecoveryCode)` (метка `staff-recovery`) — одна функция
  для выдачи и входа; сравнения — `hmac.Equal`/`subtle`. Ввод: почта — `domain.NormalizeEmail`, коды —
  `domain.NormalizeCode`/`NormalizeRecoveryCode`; своих `strings.ToLower` по почте не писать.
- **Аудит:** действие сотрудника — `ActorStaffID`, роль учётки, `ActorLabel` = `display_name`; неудача пароля —
  без автора (`anonymous`), почта в `After["email"]` (маскирует `audit.Mask`); неудача второго фактора — от
  учётки (шаг `mfa` входа — `staff.login_failed`, прочие — `staff.factor_failed`); блокировка — `system`;
  вход (`staff.login`) и разблокировка (`staff.unlock`) — с `After["method"]` = `totp` | `recovery`.
  `ScopeCityID` у записей staff пустой.
- **Письма (D3):** только задача `staff.NewMailArgs(kind, ref, recipient)` в транзакции действия
  (`river.InsertTx`), адреса и коды в аргументы не класть. `staff.locked` опирается на учётку (`locked_until`)
  и IP сессии ref, а не на причину её гашения. Составитель — `App.Compose*`: язык — `mailLocale`
  (`en-US` → `en`, неизвестный — `ru`), письмо больше не нужно — `mail.ErrSkip`; тексты — `backend/locales`
  (`<вид>.subject`, `<вид>.body`, подпись `mail.footer`).
- **Сессия (§4.7, P11, D10):** `LoadStaff` — один запрос на запрос, без кэша успеха. Истёк срок — гашение
  `expired`; Reauth — только в стадии `active` (бездействие > 20 мин или IP вне /24·/48); `last_seen_at` и `ip`
  — не чаще раза в минуту, не на фоновых запросах и не в Reauth (их меняет только Unlock).
- **«Мой доступ» (§4.7, R15, P20; `internal/app/access.go`):** `Me` — в `mfa_pending` только стадия и сессия
  (`CityIDs`, `Permissions` — пустые, не nil), в `setup` — учётка без прав, права — копия `Grants` только в
  `active`. Свои сессии — `sessionViews(staffID, current)` (общая с карточкой 8b): живые, новые сверху, IP —
  `audit.MaskIP`, пустой User-Agent — nil. Каждый запрос привязан к `s.StaffID`: чужая сессия —
  `staff.session_not_found`, как и несуществующая. Мутации принимают `staff.Meta` (IP и UA в аудит) — одна
  `db.InTx`, `a.lockSelf`: замок своей учётки, затем перепроверка сессии запроса `lockLiveSession` (погашена —
  `auth.ErrSessionInvalid`, заменена — `auth.ErrSessionReplaced`: «завершить остальные» по старой сессии не
  гасит свежую, страж `TestSelfMutationsRecheckSession`); аудит от учётки (`staff.session_revoked`,
  `staff.sessions_revoked`, `staff.password_changed`, `staff.locale_changed`). Смена пароля: выбор фактора
  (`body.code`) и `password.Check` (`body.new_password`,
  `passwordPolicyError`) и хэш нового — до транзакции; текущий пароль и фактор — в `withFactor`; успех гасит
  другие сессии (`password_changed`). Ошибки поля — `httpx.NewFieldError`.
