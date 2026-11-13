package httpx

import "slices"

// Коды ошибок платформы: их может вернуть любая операция. Каждый код — константа здесь,
// запись в PlatformCodes, строка x-error-codes-common обоих контрактов и тексты errors.<код>
// в локалях веба и админки. Сверку с контрактом держит TestPlatformCodesDocumented
// в internal/httpapi/{public,admin}.
const (
	CodeInternal                     = "internal"                       // сбой сервера; детали — только в лог
	CodeRequestInvalid               = "request.invalid"                // запрос не разбирается
	CodeRequestTooLarge              = "request.too_large"              // тело больше лимита
	CodeValidationFailed             = "validation.failed"              // нарушение схемы контракта, поля — в errors
	CodeNotFound                     = "http.not_found"                 // маршрута нет
	CodeMethodNotAllowed             = "http.method_not_allowed"        // маршрут есть, метода нет
	CodeUnauthenticated              = "auth.unauthenticated"           // 401: нет действующего access-токена или сессия отозвана
	CodeRateLimited                  = "ratelimit.exceeded"             // 429: лимит частоты, когда повторить — Retry-After
	CodeHumancheckRequired           = "humancheck.required"            // 403: нужна проверка «человек ли», задача — в challenge
	CodeIdempotencyKeyReused         = "idempotency.key_reused"         // 422: тот же Idempotency-Key с другим запросом
	CodeIdempotencyInProgress        = "idempotency.in_progress"        // 409: запрос с этим ключом ещё выполняется
	CodeIdempotencyReplayUnavailable = "idempotency.replay_unavailable" // 409: действие выполнено, ответ не сохранился — перечитать состояние
	CodeFeatureDisabled              = "feature.disabled"               // 403: функция выключена флагом (в городе)
	// 501: операция описана в контракте, но сервер её ещё не исполняет — этапы выкатываются по очереди, клиент
	// отличает «ещё нет» от «сломалось» (общий код обоих контрактов, план I-1, R1)
	CodeNotImplemented = "http.not_implemented"
)

// PlatformCodes — все коды платформы.
var PlatformCodes = []string{
	CodeInternal, CodeRequestInvalid, CodeRequestTooLarge,
	CodeValidationFailed, CodeNotFound, CodeMethodNotAllowed,
	CodeUnauthenticated, CodeRateLimited, CodeHumancheckRequired,
	CodeIdempotencyKeyReused, CodeIdempotencyInProgress, CodeIdempotencyReplayUnavailable,
	CodeFeatureDisabled, CodeNotImplemented,
}

// Коды admin-api: только в контракте admin (спека сотрудников §5.1, §7; D18), в публичный не
// попадают. Каждый — константа здесь, запись в своём списке, строка контракта admin и текст
// errors.<код> в apps/admin/messages. Сверку держат TestPlatformCodesDocumented и
// TestAdminPlatformCodesDocumented (internal/httpapi/admin) и TestAdminCodesNotInPublic
// (internal/httpapi/public).
const (
	CodeForbidden        = "auth.forbidden"              // 403: нет права на операцию или город вне скоупа
	CodeCSRFFailed       = "auth.csrf_failed"            // 403: нет Origin админки или заголовка X-WF-Admin
	CodeIPNotAllowed     = "auth.ip_not_allowed"         // 403: адрес вне ADMIN_IP_ALLOWLIST
	CodeReauthRequired   = "auth.reauth_required"        // 401: бездействие или смена сети — нужен второй фактор
	CodeSetupRequired    = "auth.setup_required"         // 403: стадия setup — сначала настройка 2FA
	CodeApprovalNotFound = "platform.approval_not_found" // 404: запроса на подтверждение нет
	CodeApprovalSelf     = "platform.approval_self"      // 409: свой запрос или запрос о себе
	CodeApprovalStale    = "platform.approval_stale"     // 409: цель изменилась после запроса
	CodeApprovalClosed   = "platform.approval_closed"    // 409: запрос уже решён, отменён или просрочен

	// флаги функций (план B, спека сотрудников §6.1, R4)
	CodeFlagNotFound     = "platform.flag_not_found"      // 404: флага с таким ключом нет
	CodeFlagStale        = "platform.flag_stale"          // 409: флаг изменили после чтения (version не совпала)
	CodeFlagCityNotFound = "platform.flag_city_not_found" // 422: несуществующий город в enabled_city_ids
)

// AdminCommonCodes — коды конвейера admin-api: их может вернуть любая операция админки
// (x-error-codes-common контракта admin вместе с PlatformCodes).
var AdminCommonCodes = []string{
	CodeForbidden, CodeCSRFFailed, CodeIPNotAllowed, CodeReauthRequired, CodeSetupRequired,
}

// AdminApprovalCodes — коды операций подтверждений вдвоём (x-error-codes approve/reject/cancel).
var AdminApprovalCodes = []string{
	CodeApprovalNotFound, CodeApprovalSelf, CodeApprovalStale, CodeApprovalClosed,
}

// AdminFlagCodes — коды операции правки флага (x-error-codes updateFeatureFlag).
var AdminFlagCodes = []string{CodeFlagNotFound, CodeFlagStale, CodeFlagCityNotFound}

// AdminPlatformCodes — все коды платформы admin-api: конвейер, подтверждения и флаги.
var AdminPlatformCodes = slices.Concat(AdminCommonCodes, AdminApprovalCodes, AdminFlagCodes)
