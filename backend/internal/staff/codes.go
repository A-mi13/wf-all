package staff

// Коды ошибок модуля для клиентов (спека сотрудников §7): x-error-codes операций тега staff в
// контракте admin, тексты — errors.<код> в apps/admin/messages. HTTP-статус выбирает хендлер (10a).
const (
	CodeInvalidCredentials = "staff.invalid_credentials" // 400: почта или пароль не подошли, учётка закрыта
	CodeMFAInvalid         = "staff.mfa_invalid"         // 400: код второго фактора не подошёл (attempts_left)
	CodePasswordInvalid    = "staff.password_invalid"    // 400: неверный текущий пароль (счётчик второго фактора)
	CodeWrongStage         = "staff.wrong_stage"         // 409: операция не для текущей стадии сессии
	CodeInviteInvalid      = "staff.invite_invalid"      // 400: код приглашения, почта или срок
	CodeEmailTaken         = "staff.email_taken"         // 409: приглашение на почту активной учётки
	CodeNotFound           = "staff.not_found"           // 404: нет сотрудника
	CodeSessionNotFound    = "staff.session_not_found"   // 404: чужая или несуществующая сессия
	CodeCityNotFound       = "staff.city_not_found"      // 422: несуществующий город в city_ids
	CodeLastOwner          = "staff.last_owner"          // 409: снять последнего владельца
	CodeSelfAction         = "staff.self_action"         // 409: действие управления над собой
)

// Codes — все коды модуля.
var Codes = []string{
	CodeInvalidCredentials, CodeMFAInvalid, CodePasswordInvalid, CodeWrongStage, CodeInviteInvalid,
	CodeEmailTaken, CodeNotFound, CodeSessionNotFound, CodeCityNotFound, CodeLastOwner, CodeSelfAction,
}
