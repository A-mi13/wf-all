package staff

// Reasons — причины выключения и сброса доступа (спека §4.9, схема StaffActionReason): код из списка
// и необязательный комментарий. В аудит пишется «код» или «код: комментарий», в письмо
// staff.admin_disabled — только переведённая метка кода (staff.reasons.<код>).
var Reasons = []string{"left_team", "security", "role_change", "other"}

// AccountFilter — фильтр списка «Роли и доступ» (listStaffAccounts): статус и роль (пусто — все), q —
// начало почты или имени без учёта регистра, курсор и размер страницы (page.Limit).
type AccountFilter struct {
	Status, Role, Q, Cursor string
	Limit                   int
}

// RolesView — роли и матрица прав без черновых строк (getStaffRoles, схема StaffRoles).
type RolesView struct {
	Roles       []string // по старшинству (perm.Roles)
	Permissions []RolePermission
}

// RolePermission — строка матрицы: право, меняет ли данные и доступ каждой роли (allow | scoped | dual |
// deny — auth.Grant.String()).
type RolePermission struct {
	Key      string
	Mutating bool
	Grants   map[string]string
}
