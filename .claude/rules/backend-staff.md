---
paths:
  - "backend/internal/staff/**"
---

# Модуль staff (сотрудники админки)

Спека — `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §4; решения плана A —
`.superpowers/sdd/2026-10-06-staff-a/decisions-r2.md` (D7–D12) и приложение Б плана (P1–P11).

- **Пакеты без циклов (P1–P5).** Корень `staff` — только `Deps`, `Meta`, DTO, ошибки (`Err*`, `MFAError`,
  `RateLimitError`, `ResendTooSoonError`, `CodeOf`), коды (`codes.go`, сверка с контрактом —
  `TestStaffCodesDocumented` в `admin`), события (`events.go`) и задача письма (`MailArgs`, `NewMailArgs`,
  очередь `queue.Staff`); корень не импортирует `internal/app`, `admin`, `jobs` (страж
  `TestRootDoesNotImportInnerPackages`). SQL — только `staff_*` (`queries/` → `internal/store/staffdb`,
  генерация — `go tool -modfile=tools/go.mod sqlc generate`).
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
  `domain.NormalizeCode` (TOTP)/`NormalizeRecoveryCode`/`NormalizeInviteCode`; своих `strings.ToLower` по почте не писать.
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
- **Приглашения (§4.3–4.4, D4, D12; решение пользователя 14.10.2026; `internal/app/invite.go`):** код —
  `domain.NewInviteCode` (`xxxxx-xxxxx`, 10 символов base32 RFC 4648 строчными, 50 бит; общий генератор с
  резервными кодами), ввод — `domain.NormalizeInviteCode` (нормализация резервных кодов); в базе только
  `keys.Ring.Seal(код, aad = id приглашения)` (`code_enc`, `code_key_id`), сверка — `inviteCodeMatches` (`Open`,
  нормализация, `subtle.ConstantTimeCompare`); открытого кода нет в базе, аргументах задач и аудите (страж
  `TestNoSecretsInAuditAndJobs`). Неверный код ничего не стирает и в строке не считается (счётчика попыток в
  `staff_invites` нет): иначе знающий почту анонимно срывал приглашение; живое приглашение всегда с кодом
  (`CHECK staff_invites_live_has_code`, стереть — только с `revoked_at`). Нечитаемый код (ключ убран раньше
  rekey) — `readInviteCode` ok=false с записью в журнал сервера без кода (анонимное принятие — Debug, письмо —
  Warn): принятие — `staff.invite_invalid`, письмо — `mail.ErrSkip` (страж `TestInviteUnreadableCode`);
  самоотправка лечит его — `healInviteCode`: новый код текущим ключом в той же строке, срок прежний, Warn
  (`TestResendInviteSelfHealsUnreadableCode`). Анонимные `ResendInviteSelf` и `AcceptInvite` сначала читают кандидата без
  замков (`GetInviteCandidate`): делать нечего — ответ без замков (у принятия — одна запись неудачи, у
  повторной отправки — ни одной; время не выдаёт существование почты, страж `TestInviteIneligibleTakesNoLocks`); подходящему — замок учётки, затем строки приглашения, и
  проверка заново. Остальные сценарии с приглашением берут замок учётки, затем строку приглашения. `IssueInvite(ctx, tx, staffID, purpose)` — в транзакции вызывающего (учётка уже под замком):
  гасит живое (код стирается), новое на 24 ч, `sends = 1`, письмо `staff.NewMailArgs(вид по назначению, id
  приглашения, учётка)`. Повторная отправка самим (`ResendInviteSelf`) — всегда nil; ТОТ ЖЕ код
  (`MarkInviteResent`: `sends + 1`, `last_sent_at`, строка не перешифровывается, срок прежний; составитель
  расшифрует код сам) только при живом приглашении с кодом, паузе 60 с и `sends < 5` («сутки» = жизнь строки:
  самостоятельная отправка срок не продлевает); аудит `anonymous`; страж ротации — `TestResendInviteSelfSameCode`. Различимость
  по времени у самоотправки (подходящая почта — транзакция с замками, задача и аудит) принята; выравнивать не будем —
  объём ограничивают IP-лимит, пауза 60 с и не больше 5 отправок. Админом
  (`ResendInvite`) — `Decide(staff.invites.resend) == Allow`, не над собой, только `invited`, пауза —
  `*staff.ResendTooSoonError` (429 с остатком) — от выпуска последнего приглашения (`GetLastInvite.created_at`),
  не от `last_sent_at`: самоотправки паузу админа не двигают (`TestResendInvitePauseFromIssue`); новое
  приглашение того же назначения на 24 ч. Принятие
  (`AcceptInvite`): `password.Check` (`body.password`) и хэш — до транзакции; почта, статус, срок, нет кода
  и неверный код — один `staff.ErrInviteInvalid`. Сверка — по чтению без замков (`GetInviteCandidate` отдаёт
  шифротекст): ЛЮБОЙ исход «принять нечего» (кривая или чужая почта, не `invited`, нет приглашения, срок,
  неверный или нечитаемый код) — `a.acceptFailed`: одна запись `staff.invite_accept_failed` (`anonymous`,
  `After = {email}`) в короткой транзакции БЕЗ замков на `detached`, не больше `inviteAuditPolicy` (всплеск 20,
  затем одна в час — не больше 20 за час, `TestAcceptInviteFailureAuditHourly`) на почту по `ratelimit.EmailKey("staff_invite_audit", HMAC)`, сверх — без записи; верный код потолок не
  трогает; хранилище лимитов недоступно — запись идёт (`TestLimiterDownStillAudits`, там же вход) (стражи `TestAcceptInviteFailureAuditedAlways`, `TestAcceptInviteFailureAuditCap`, `TestAcceptInviteWrongCodeKeepsCode`,
  `TestAcceptInviteConcurrentWrong`, `TestAcceptInviteCanceledAfterHash`, путь «неверный код» в
  `TestInviteIneligibleTakesNoLocks`); верный — замки учётки и строки, `lockAcceptable` и сверка с кодом
  закрытой замком строки (перевыпуск во время ожидания — старый код неверен,
  `TestAcceptInviteReissuedWhileWaiting`). Успех: пароль,
  блокировка второго фактора снята, `staff_mfa` и коды удалены, все сессии учётки и сессия старой cookie
  (чья угодно) — `replaced`, новая — `setup` на 8 ч; приглашение живо до ack. Учётка `invited` с ролью и
  городами — `createInvited` (почта и имя — `validateInvitee`, занята — `ErrEmailTaken`); автор аудита —
  `actor` (`staffActor(s)` или `operatorActor`).
- **Настройка 2FA (§4.5; `internal/app/setup.go`):** стадия `setup` — `lockSetup` (учётка → сессия;
  `lockLiveSession` не подходит — он требует `active`): погашена — `ErrSessionInvalid`, заменена —
  `ErrSessionReplaced`, не та стадия — `staff.wrong_stage`. `MFASetup` заменяет неподтверждённый секрет
  (`sealTOTP`), подтверждённый — `wrong_stage`; издатель otpauth — хост `ADMIN_ORIGIN` без порта. `MFAConfirm`:
  неверный код — `staff.ErrMFAInvalid` без счётчика (не `withFactor`: учётка ещё `invited`); успех —
  `last_used_step`, `confirmed_at`, коды `issueRecoveryCodes` (тот же `recoveryMAC`, что при входе).
  `RecoveryAck`: гасит не погашенное приглашение без проверки срока (владение кодом доказано на accept, у
  сессии setup свой срок — иначе тупик на границе 24 ч); живого нет — `wrong_stage`; учётка `active`, новый токен через
  `replaceSession` (срок — от принятия), аудит `staff.mfa_enabled`. Перевыпуск кодов (`RegenerateRecovery`) —
  только TOTP через `withFactor` (шаг `recovery_regenerate`), страж погашенной сессии
  `TestRegenerateRecoveryRevokedSession`.
- **Управление (§2.5–2.6, §4.9, D16, D17, P13; `internal/app/{manage,actions,accounts}.go`):** сценарий решает
  по `Decide`: `Allow` — сразу, `NeedsApproval` — запрос `a.request` (202), `Deny` — 403 `errForbidden`.
  Приглашение — всегда запрос (почта active/invited — `email_taken`, выключенной — `existing_id` + её версия,
  исполнение возвращает ту же учётку сбросом доступа с новой ролью, именем и приглашением `invite`); смена роли —
  таблица D16 (`rolePermission`; без изменений — `nil, nil`); выключение — право по роли цели (владелец — только
  запрос `staff.revoke_owner`); сброс доступа — всегда запрос. Выключенная цель смены роли и сброса — 403 (вернуть
  её — только приглашением). Над собой — `self_action`; `confirm` — `domain.NormalizeEmail` и сравнение с почтой
  цели (`mismatch`); `reason` — `staff.Reasons`, комментарий ≤ 500 — только в `audit_log.reason`
  («код: комментарий»), не в before/after.
  - **Порядок замков (m7, M-4):** `lockOwners` (`pg_advisory_xact_lock(ownersLockKey)` — только где возможен страж
    последнего владельца: выключение, сброс, смена роли, `OperatorDisable`; всегда первым) → `lockAccounts` — учётки
    по возрастанию id (цель `FOR UPDATE`, автор/инициатор/подтверждающий `FOR SHARE`) → сессии → `staff_mfa` →
    коды → приглашения. Автор и подтверждающий — `checkActor` под замком: учётка не active/закрыта, сессия погашена
    или истекла — `auth.ErrSessionInvalid`, роль сменилась — 403 (стражи `TestManageActorRechecked`,
    `TestManageActorAccountState`, `TestManageActorSessionRevoked`, `TestApproverRechecked`); тот же шаблон в
    `ResendInvite`. Встречные действия без 40P01 — `TestCounterRevokeSessionsQueued`,
    `TestCounterRequestsQueuedOnOwnersLock`.
  - **Страж последнего владельца** — `ownerGuard` после `lockOwners`: при запросе (ранний отказ) и в исполнении,
    в `disable` (и `OperatorDisable`), понижении (`applyRole`), сбросе; кроме `OperatorResetAccess` (выход из тупика).
    В пути подтверждения он защита в глубину: инициатор и подтверждающий — активные владельцы под замком, поэтому
    `last_owner` там недостижим; достижим у оператора (`TestLastOwnerConcurrent`, `-count=30`).
  - **Версия учётки** растёт при выключении, сбросе, смене роли/городов, повторном приглашении (и `LockOut`).
    payload каждого действия — `v`, `init_id`, `init_ver` (+ цель и её `version`). `checkInitiator`
    (`InitiatorCheck`) — без замка; исполнитель (`execLocks`) повторяет её под `FOR SHARE` и перепроверяет
    подтверждающего; инициатор изменился — `ErrInitiatorChanged` (`initiator_changed`), цель другой версии —
    `ErrStale` (`platform.approval_stale`). Блокировка (`LockOut`) инициатора или цели валит запрос —
    осознанно (`TestLockOutTargetStale`). Бизнес-отказ исполнителя — только `approvals.Fail` (`business`:
    `last_owner`, `email_taken`, `city_not_found` — в т.ч. FK 23503 города) или `ErrStale`.
  - Выключение, сброс и смена роли гасят ожидающие запросы сотрудника (`cancelRequests` → `CancelByInitiator`).
  - **Письмо U1** — `disable` админа: `staff.NewMailArgs(MailAdminDisabled, auditID, владелец)` каждому активному
    владельцу, кроме инициатора (оператор — всем); `auditID` — `Entry.ID` записи `staff.disabled`.
    `ComposeAdminDisabled` — `audit.Get`, причина — метка `staff.reasons.<код>` (код до `": "`), `operator` —
    «оператор сервера», неизвестный код — без строки причины; `ErrSkip`: не `staff.disabled`, нет учётки,
    получатель не активный владелец.
  - Список — новые сверху, `page.Keyset{Set: "staff.accounts(created_at,id)"}`, `q` — начало почты или имени
    (`likePrefix`: `\`, `%`, `_` буквально); чужие сессии владельца и админа — только при `grant_elevated` не Deny
    (админу — 403, страж через HTTP `TestAccountSessionsAdminScope`).
  - **`PUT …/role` без `version`** (контракт плана A): правка из устаревшей панели снимает город, добавленный другим
    сотрудником, — известное ограничение: последняя запись побеждает, след — аудит `staff.role_changed` (было/стало).
  - **Хендлеры (`staff/admin/accounts.go`, Task 10b):** 202 `{approval}` — `oapi.ApprovalOf` (ручной файл в
    `httpapi/admin/oapi`: общий для `staff/admin` и сборки); `ChangeRole`/`Disable` сразу (`nil, nil`) — 200 с
    перечитанной карточкой `Account` (`committedAccount`: ctx без отмены и свой срок 5 с — обрыв после коммита не даёт
    500 при выполненном действии, страж `TestImmediateActionAnswersAfterCancel`). `RegisterApprovals(reg, svc)` — обёртка `app.RegisterApprovals` для сборки 10c;
    `Label(ctx, s)` — подпись решившего для подтверждений (`App.Label`: учётка и сессия перечитываются без замка, как
    `checkActor`; approve повторяет под замком в исполнителе, reject и cancel — нет).
- **Оператор (§4.2, §4.11; `internal/app/operator.go`):** аудит — `actor_role = operator`, метка `operator`.
  `OperatorBootstrap` — учётка `owner` (`invited`), приглашение, `staff.bootstrapped`, событие `staff.invited`,
  письмо `staff.bootstrapped` каждому активному владельцу; доступен всегда. `OperatorInviteResend` — без паузы и
  прав. `OperatorResetAccess` — сброс без стража последнего владельца (любой статус); `OperatorDisable` — под
  стражем, письмо U1 о выключенном админе всем активным владельцам. `Rekey` — каждая строка (секрет TOTP, код приглашения) не на текущем ключе — своей транзакцией под
  замком учётки; нечитаемая (ключ убран раньше времени) не роняет прогон — `UnreadableMFA`/`UnreadableInvites`
  с id; резервные коды только считаются; после прогона — `MFAOnOldKeys`, `InvitesOnOldKeys`,
  `RecoveryOnOldKeys`; итог (и частичный) — аудит `staff.rekeyed`. Ротация ключа — двухфазная, чтобы при
  скользящем деплое ни один экземпляр не встретил шифротекст на незнакомом ключе: (1) выкатить новый ключ
  ВТОРЫМ в `ADMIN_STAFF_SECRET_KEYS` во все экземпляры admin-api (все читают его, шифруют ещё старым);
  (2) выкатить новый ключ ПЕРВЫМ (старый — вторым) → `rekey`; (3) перед удалением старого ключа повторный
  `rekey` — убирать только при нулях во всех трёх счётчиках и пустых списках нечитаемых.
  Ключ убран раньше времени: письма приглашений за это окно отменяются (`mail.ErrSkip`, предупреждение в
  журнал сервера), не повторяются; восстановление — самоотправка сотрудника (код перевыпускается в той же
  строке) или повторная отправка админом/оператором. Процедура для оператора — `docs/deploy-dev.md`.
  Подкоманды `admin-api staff …` (Task 10c) идут через обёртки `staff/admin/operator.go` (cmd/admin-api не видит
  `internal/app`): код приглашения отдаётся наружу **только** при `printCode` (`onlyIf`) — без флага его нет даже у
  вызывающего; `RekeyReport` — копия всех полей итога + `Clean()`. Вывод команды — stdout, почта маской
  (`logx.Mask`), код — только с `--print-code`, в журнал и stderr — никогда (страж `TestOperatorCommands`).
  Команды, выпускающие код (`bootstrap`, `invite-resend`, `reset-access`), сначала сверяют ключи базы с кольцом
  (`App.UnknownKeys`: `key_id` секретов TOTP, резервных кодов и живых кодов приглашений, которых нет в кольце; запрос
  `ListStoredKeyIDs`) — есть чужие — отказ до записи, код 1, без ключей в выводе (`TestOperatorRefusesForeignKeys`).
- **Фон (§4.10, Task 10c; `staff/jobs`):** `MailWorker` (`AddWorkers(w, d, sender)` — исполняющий River-клиент
  admin-api на очереди `staff`): вид → составитель (`invite`/`access_reset` → `ComposeInvite(…, kind)`, `locked`,
  `bootstrapped`, `admin_disabled`); `mail.ErrSkip` — `river.JobCancel` (без повторов) и строка журнала с видом и id, без
  почты; неизвестный вид, `V != 1` и ошибка SMTP — обычная ошибка (повтор: при скользящем деплое письмо нового вида
  возьмёт экземпляр нового релиза); таймаут задачи — минута.
  `Deps.River` сценариев — отдельный клиент только для вставки (иначе цикл «исполнителю нужны Deps»). `Cleaner`:
  `Tick` = `App.Cleanup` (сессии, умершие — `LEAST(expires_at, revoked_at)` — больше `CleanupRetention` = 30 суток
  назад; приглашения, погашенные/отозванные/просроченные раньше границы, кроме последнего у сотрудника — по нему
  `GetLastInvite` берёт назначение; порции `FOR UPDATE SKIP LOCKED`, строку под замком сценария пропускают, страж
  `TestCleanupSkipsLockedRows`) + `ExpireDue` + `PurgeDecided(DecidedRetention)`; `Run` — проход сразу, затем раз
  в час; шаги независимы (сбой одного не отменяет другие, ошибки вместе — `TestCleanupStepsIndependent`); ошибка
  прохода — `log.Error`, тикер живёт; отмена ctx — не сбой.
- **Хендлеры (`staff/admin`, Task 10a):** тонкие — запрос → сценарий `internal/app` → ответ контракта; логики
  сценариев в них нет. `New(staff.Deps, Cookie{Name, Secure})` (имя и `Secure` — от сборки:
  `httpapi/admin.CookieName`; `CookieConfig()` — для сверки сборкой, рассогласование — отказ старта), `Sessions()` — загрузчик для `Options.Sessions`, `Middleware()` — strict-middleware
  (подключает `httpapi/admin`), только для операций тега staff (методы `*Handler` вида `(ctx, oapi.…RequestObject)` — `IsOperation`): нулевой `Handler` (main до
  10c) — `501 http.not_implemented` без вызова метода; User-Agent и токен cookie — в ctx (`meta(ctx)`,
  `requestFrom(ctx).token` → `oldToken` принятия); ошибки — **только** `staffadmin.Problem` (единственный перевод:
  `ProblemError` как есть, `*MFAError` → `mfa_invalid`/`password_invalid` с `attempts_left` через
  `httpx.(*Error).WithAttemptsLeft`, коды модуля — статусы `statusOf` (страж `TestProblemCoversStaffCodes`),
  `auth.ErrSessionInvalid`/`ErrSessionReplaced` → 401). Cookie: `ErrSessionInvalid` (сессию погасили между
  конвейером и замком) стирает её, как конвейер; `ErrSessionReplaced` — нет (в браузере уже новая). Новый токен
  (вход, принятие, второй фактор, unlock, ack) и стирание у выхода — `cookieResponse` через `http.SetCookie`
  (добавление): сгенерированный `Visit…` делает `Header().Set` и затёр бы стирание старой cookie от конвейера —
  новая cookie идёт последней (`TestStaffLoginAfterInvalidCookie`). Ответы с секретом (setup, confirm,
  перевыпуск кодов) заполняют `Headers.CacheControl = "no-store"`: пустое значение генератора стёрло бы
  `no-store` конвейера. Код модуля наружу — только из `x-error-codes` операции: недостижимая ветка сценария —
  внутренняя ошибка (500), не `staff.*` (`errPasswordNotActive`). D9 у входа — сценарий `Login` (сотрудник конвейера в ctx), отдельный `Logout` не звать:
  под `Idempotency-Key` — ровно одна транзакция. Отзыв текущей сессии cookie не стирает. Сквозные тесты — в
  `internal/httpapi/admin` (`staff_*_test.go`: `staff/admin` не импортирует `httpapi/admin` даже в тестах).
