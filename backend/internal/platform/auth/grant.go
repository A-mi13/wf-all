package auth

// Grant — значение права роли сотрудника в матрице прав (platform/perm, docs/05-ролевая-модель.md).
// Нулевое значение — GrantDeny: чего нет в правах роли, того нельзя. Числа констант не хранятся и
// наружу не уходят (наружу — String), но порядок не менять: новые значения — только в конец.
type Grant uint8

const (
	// GrantDeny — нельзя.
	GrantDeny Grant = iota
	// GrantAllow — можно; у права чтения — читать всё.
	GrantAllow
	// GrantScoped — можно только в городах модератора.
	GrantScoped
	// GrantDual — только вдвоём: операция создаёт подтверждение, исполняет его второй сотрудник.
	GrantDual
)

// String — значение для документа, контракта и ответа GET /v1/staff/me: allow, scoped, dual, deny.
// Незнакомое число — deny: ошибка в сторону отказа.
func (g Grant) String() string {
	switch g {
	case GrantAllow:
		return "allow"
	case GrantScoped:
		return "scoped"
	case GrantDual:
		return "dual"
	default:
		return "deny"
	}
}
