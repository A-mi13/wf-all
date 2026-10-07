// Package perm — каталог прав сотрудников и матрица ролей (docs/05-ролевая-модель.md; спека среза
// «Сотрудники и админка geo» §2): ключи прав, значения по ролям, права роли для сессии сотрудника,
// таблица документа и каталог для стража контракта admin. Модуль, которому нужны свои права,
// добавляет строки в rows своим коммитом и запускает ./task gen.
package perm

import (
	"maps"

	"wf/backend/internal/platform/auth"
)

// Role — роль сотрудника (staff_roles.role). У сотрудника ровно одна роль.
type Role string

const (
	Owner     Role = "owner"     // владелец: всё, включая выдачу прав
	Admin     Role = "admin"     // админ: операционное управление
	Moderator Role = "moderator" // модератор: очереди, жалобы, ограничения — в своих городах
	Support   Role = "support"   // поддержка: только чтение
)

// Roles — все роли в порядке столбцов матрицы.
var Roles = []Role{Owner, Admin, Moderator, Support}

// Служебные значения x-permission — не права каталога (docs/05, «Где проверяется право»).
const (
	Anonymous = "anonymous" // без сессии; у операции security: []
	Session   = "session"   // сессия любой стадии; стадию проверяет сценарий
	Self      = "self"      // активная сессия без Reauth; своё («Мой доступ»)
	Handler   = "handler"   // право зависит от тела или цели: x-permission-any, точно решает сценарий
)

// Service — служебные значения x-permission в порядке документа.
var Service = []string{Anonymous, Session, Self, Handler}

// Permission — право каталога.
type Permission struct {
	Key      string // <модуль>.<ресурс>.<действие>
	Title    string // по-русски, для таблицы docs/05
	Mutating bool   // меняет состояние; false — только чтение (действие read или search)
	Draft    bool   // строка будущего модуля: только в документе; в контракте и правах ролей её нет
}

const (
	deny   = auth.GrantDeny
	allow  = auth.GrantAllow
	scoped = auth.GrantScoped
	dual   = auth.GrantDual
)

// row — строка матрицы: право и значения по ролям в порядке Roles.
type row struct {
	p Permission
	g [4]auth.Grant
}

func reading(key, title string, owner, admin, moderator, support auth.Grant) row {
	return row{Permission{Key: key, Title: title}, [4]auth.Grant{owner, admin, moderator, support}}
}

func changing(key, title string, owner, admin, moderator, support auth.Grant) row {
	return row{Permission{Key: key, Title: title, Mutating: true}, [4]auth.Grant{owner, admin, moderator, support}}
}

// draft — строка будущего модуля (макет 07, «Роли и доступ»): значения уточнит спека модуля.
func draft(r row) row {
	r.p.Draft = true
	return r
}

// rows — каталог в порядке документа: права среза (спека §2.5 дословно), затем черновые.
// Столбцы: владелец, админ, модератор, поддержка.
var rows = []row{
	// staff — сотрудники и роли
	reading("staff.roles.read", "матрица ролей и прав", allow, allow, allow, allow),
	reading("staff.accounts.read", "список сотрудников и карточка", allow, allow, deny, deny),
	changing("staff.roles.grant_elevated", "выдать роль владельца или админа", dual, deny, deny, deny),
	changing("staff.roles.grant", "выдать роль модератора или поддержки, добавить модератору город", dual, dual, deny, deny),
	changing("staff.roles.revoke_owner", "снять владельца (выключить учётку)", dual, deny, deny, deny),
	changing("staff.roles.revoke_admin", "снять админа (выключить учётку)", allow, deny, deny, deny),
	changing("staff.roles.revoke", "снять модератора или поддержку, убрать модератору город", allow, allow, deny, deny),
	changing("staff.access.reset_elevated", "сбросить доступ владельцу или админу", dual, deny, deny, deny),
	changing("staff.access.reset", "сбросить доступ модератору или поддержке", dual, dual, deny, deny),
	changing("staff.invites.resend", "отправить приглашение повторно", allow, allow, deny, deny),
	changing("staff.sessions.revoke", "завершить чужие сессии (админ — только модераторов и поддержки)", allow, allow, deny, deny),
	// журнал действий
	reading("audit.log.read", "читать журнал действий (модератор — свои города и свои записи)", allow, allow, scoped, allow),
	// geo — справочник городов
	reading("geo.places.search", "искать места GeoNames", allow, allow, deny, deny),
	reading("geo.cities.read", "города и районы", allow, allow, scoped, allow),
	changing("geo.cities.activate", "открыть город", allow, allow, deny, deny),
	changing("geo.cities.manage", "статус, slug и привязка geoname_id города", allow, allow, deny, deny),
	changing("geo.cities.rename", "названия города по языкам", allow, allow, scoped, deny),
	changing("geo.districts.manage", "районы города", allow, allow, scoped, deny),
	reading("geo.countries.read", "страны и их правила", allow, allow, allow, allow),
	changing("geo.countries.manage", "правила страны и её включение", allow, allow, deny, deny),
	reading("geo.imports.read", "журнал импорта GeoNames", allow, allow, deny, allow),
	changing("geo.imports.run", "запустить импорт GeoNames", allow, allow, deny, deny),
	// platform — версии приложения и флаги функций
	reading("platform.versions.read", "версии приложения", allow, allow, allow, allow),
	changing("platform.versions.manage", "минимальная и рекомендуемая версия, ссылка на стор", allow, allow, deny, deny),
	reading("platform.flags.read", "флаги функций", allow, allow, allow, allow),
	changing("platform.flags.manage", "переключать флаги функций по городам", allow, allow, deny, deny),

	// черновые строки будущих модулей (макет 07, «Роли и доступ»)
	draft(changing("teams.captains.approve", "одобрять заявки на капитанство", allow, allow, scoped, deny)),
	draft(changing("pitches.pitches.moderate", "публиковать и объединять площадки", allow, allow, scoped, deny)),
	draft(reading("moderation.reports.read", "читать жалобы и споры", allow, allow, scoped, allow)),
	draft(changing("moderation.reports.resolve", "решать жалобы и споры с формулировкой", allow, allow, scoped, deny)),
	draft(changing("moderation.restrictions.apply", "накладывать ограничения до 30 дней", allow, allow, scoped, deny)),
	draft(changing("moderation.bans.issue", "блокировать навсегда, без срока", dual, dual, deny, deny)),
	draft(changing("results.reports.annul", "аннулировать протокол матча (снятие голов у всех)", dual, dual, deny, deny)),
	draft(reading("identity.pii.read", "видеть почту и телефон игрока (каждое чтение — в журнал)", allow, allow, scoped, allow)),
	draft(changing("economy.catalog.manage", "каталог косметики, подписок и наград", allow, allow, deny, deny)),
	draft(changing("economy.credits.grant", "ручное начисление кредитов и очков", dual, dual, deny, deny)),
}

var catalog, matrix, byKey, grants = build(rows)

// Catalog — все права в порядке строк документа. Только для чтения.
var Catalog = catalog

// Matrix — ключ права → роль → значение, полная по Catalog × Roles (черновые строки тоже). Только для
// чтения.
var Matrix = matrix

func build(rs []row) ([]Permission, map[string]map[Role]auth.Grant, map[string]Permission, map[Role]map[string]auth.Grant) {
	catalog := make([]Permission, 0, len(rs))
	matrix := make(map[string]map[Role]auth.Grant, len(rs))
	index := make(map[string]Permission, len(rs))
	byRole := make(map[Role]map[string]auth.Grant, len(Roles))
	for _, r := range Roles {
		byRole[r] = map[string]auth.Grant{}
	}
	for _, rw := range rs {
		if _, dup := index[rw.p.Key]; dup {
			panic("perm: право " + rw.p.Key + " объявлено дважды")
		}
		catalog = append(catalog, rw.p)
		index[rw.p.Key] = rw.p
		values := make(map[Role]auth.Grant, len(Roles))
		for i, role := range Roles {
			values[role] = rw.g[i]
			if !rw.p.Draft && rw.g[i] != auth.GrantDeny {
				byRole[role][rw.p.Key] = rw.g[i]
			}
		}
		matrix[rw.p.Key] = values
	}
	return catalog, matrix, index, byRole
}

// Grants — права роли для сессии сотрудника (auth.Staff.Grants): без черновых строк и без отказов
// (нет ключа — GrantDeny). Копия: вызывающий хранит её у себя, чужие правки её не трогают (P12).
// Незнакомая роль — пустая карта.
func Grants(r Role) map[string]auth.Grant {
	if g, ok := grants[r]; ok {
		return maps.Clone(g)
	}
	return map[string]auth.Grant{}
}

// Lookup — право каталога по ключу, включая черновые.
func Lookup(key string) (Permission, bool) {
	p, ok := byKey[key]
	return p, ok
}
