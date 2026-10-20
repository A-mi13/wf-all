package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Чтение «Роли и доступ» (§4.9): список и карточка сотрудников, чужие сессии, матрица ролей.

// accountsSet — набор полей курсора списка сотрудников (page.Keyset).
const accountsSet = "staff.accounts(created_at,id)"

// Поля запроса списка для 400 validation.failed.
const (
	fieldQueryStatus = "query.status"
	fieldQueryRole   = "query.role"
)

// statuses — состояния учётки (staff_status, StaffStatus контракта).
var statuses = []string{statusInvited, statusActive, statusDisabled}

// accountRow — строка учётки списка или карточки.
type accountRow struct {
	ID                       uuid.UUID
	Email, Name, Locale      string
	Status, Role             string
	LastLoginAt, LockedUntil *time.Time
	CreatedAt                time.Time
	Version                  int64
}

// Accounts — страница сотрудников (listStaffAccounts): право staff.accounts.read (иначе 403); новые
// сверху (created_at, id), курсор page.Keyset; статус и роль — из списков (иначе 400 query.status |
// query.role, enum); q — начало почты или имени без учёта регистра (%, _ и \ — буквально). next — курсор
// следующей страницы ("" — страниц больше нет).
func (a *App) Accounts(ctx context.Context, s *auth.Staff, f staff.AccountFilter) ([]staff.AccountView, string, error) {
	if s.Decide(permAccountsRead, uuid.Nil) != auth.Allow {
		return nil, "", errForbidden
	}
	if f.Status != "" && !slices.Contains(statuses, f.Status) {
		return nil, "", httpx.NewFieldError(fieldQueryStatus, ruleEnum)
	}
	if f.Role != "" && !slices.Contains(perm.Roles, perm.Role(f.Role)) {
		return nil, "", httpx.NewFieldError(fieldQueryRole, ruleEnum)
	}
	limit := page.Limit(&f.Limit)
	arg := staffdb.ManageListAccountsParams{
		Status: optString(f.Status), Role: optString(f.Role), Q: likePrefix(f.Q),
		Lim: int32(limit + 1), //nolint:gosec // page.Limit ≤ 100
	}
	if f.Cursor != "" {
		k, err := page.DecodeKeyset(f.Cursor, accountsSet, page.KindTime, page.KindUUID)
		if err != nil {
			return nil, "", err
		}
		arg.HasCursor, arg.CursorAt, arg.CursorID = true, k.Values[0].(time.Time), k.Values[1].(uuid.UUID)
	}
	q := staffdb.New(a.pool)
	rows, err := q.ManageListAccounts(ctx, arg)
	if err != nil {
		return nil, "", err
	}
	var next string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = page.EncodeKeyset(page.Keyset{Set: accountsSet, Values: []any{last.CreatedAt, last.ID}},
			page.KindTime, page.KindUUID)
	}
	list := make([]accountRow, 0, len(rows))
	for _, r := range rows {
		list = append(list, accountRow{ID: r.ID, Email: r.Email, Name: r.DisplayName, Locale: r.Locale, Status: r.Status,
			Role: r.Role, LastLoginAt: r.LastLoginAt, LockedUntil: r.LockedUntil, CreatedAt: r.CreatedAt, Version: r.Version})
	}
	views, err := a.accountViews(ctx, q, list)
	if err != nil {
		return nil, "", err
	}
	return views, next, nil
}

// Account — карточка сотрудника (getStaffAccount): право staff.accounts.read; нет — staff.not_found.
func (a *App) Account(ctx context.Context, s *auth.Staff, staffID uuid.UUID) (staff.AccountView, error) {
	if s.Decide(permAccountsRead, uuid.Nil) != auth.Allow {
		return staff.AccountView{}, errForbidden
	}
	q := staffdb.New(a.pool)
	r, err := q.ManageGetAccount(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return staff.AccountView{}, staff.ErrNotFound
	}
	if err != nil {
		return staff.AccountView{}, err
	}
	views, err := a.accountViews(ctx, q, []accountRow{{ID: r.ID, Email: r.Email, Name: r.DisplayName, Locale: r.Locale,
		Status: r.Status, Role: r.Role, LastLoginAt: r.LastLoginAt, LockedUntil: r.LockedUntil, CreatedAt: r.CreatedAt,
		Version: r.Version}})
	if err != nil {
		return staff.AccountView{}, err
	}
	return views[0], nil
}

// accountViews — учётки с городами модератора (у остальных — пустой список) и живым приглашением
// (назначение, срок, просрочено — now ≥ expires_at, выпуск).
func (a *App) accountViews(ctx context.Context, q *staffdb.Queries, rows []accountRow) ([]staff.AccountView, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	cities, err := q.ManageCitiesOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	byStaff := map[uuid.UUID][]uuid.UUID{}
	for _, c := range cities {
		byStaff[c.StaffID] = append(byStaff[c.StaffID], c.CityID)
	}
	invites, err := q.ManageLiveInvites(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := a.clock.Now()
	live := map[uuid.UUID]*staff.InviteState{}
	for _, i := range invites {
		live[i.StaffID] = &staff.InviteState{Purpose: i.Purpose, ExpiresAt: i.ExpiresAt.UTC(),
			Expired: !now.Before(i.ExpiresAt), IssuedAt: i.CreatedAt.UTC()}
	}
	out := make([]staff.AccountView, 0, len(rows))
	for _, r := range rows {
		cs := append([]uuid.UUID{}, byStaff[r.ID]...)
		out = append(out, staff.AccountView{
			ID: r.ID, Email: r.Email, DisplayName: r.Name, Locale: r.Locale, Status: r.Status, Role: r.Role,
			CityIDs: cs, CreatedAt: r.CreatedAt.UTC(), LastLoginAt: utcPtr(r.LastLoginAt), LockedUntil: utcPtr(r.LockedUntil),
			Version: r.Version, Invite: live[r.ID],
		})
	}
	return out, nil
}

// AccountSessions — живые сессии сотрудника (listStaffAccountSessions): право staff.accounts.read; нет
// учётки — staff.not_found; сессии владельца или админа — только тому, кто может выдавать эти роли
// (grant_elevated не Deny; админу — 403, и на свою учётку тоже: свои сессии админ видит в «Мой доступ»).
// Читают только владелец и админ (accounts.read): у владельца на свою учётку текущая сессия отмечена, в
// чужих карточках — никогда.
func (a *App) AccountSessions(ctx context.Context, s *auth.Staff, staffID uuid.UUID) ([]staff.SessionView, error) {
	if s.Decide(permAccountsRead, uuid.Nil) != auth.Allow {
		return nil, errForbidden
	}
	r, err := staffdb.New(a.pool).ManageGetAccount(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, staff.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if elevated(r.Role) && s.Decide(permGrantElevated, uuid.Nil) == auth.Deny {
		return nil, errForbidden
	}
	current := uuid.Nil
	if staffID == s.StaffID {
		current = s.SessionID
	}
	return a.sessionViews(ctx, staffID, current)
}

// RevokeAccountSessions — завершить все живые сессии другого сотрудника (revokeStaffAccountSessions):
// право staff.sessions.revoke (иначе 403); над собой — staff.self_action (свои — «Мой доступ»); нет
// учётки — staff.not_found; владельца или админа — только тому, кто может выдавать эти роли (админу —
// 403). Автор перепроверяется под замком (M-4). Возвращает, сколько завершено; аудит
// staff.sessions_revoked от автора (0 — без записи).
func (a *App) RevokeAccountSessions(ctx context.Context, s *auth.Staff, staffID uuid.UUID, m staff.Meta) (int, error) {
	if s.Decide(permSessionsRevoke, uuid.Nil) != auth.Allow {
		return 0, errForbidden
	}
	if staffID == s.StaffID {
		return 0, staff.ErrSelfAction
	}
	var n int64
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		locked, err := lockAccounts(ctx, q, staffID, s.StaffID)
		if err != nil {
			return err
		}
		now := a.clock.Now()
		by, err := checkActor(ctx, q, s, locked, now)
		if err != nil {
			return err
		}
		t, ok := locked[staffID]
		if !ok {
			return staff.ErrNotFound
		}
		if elevated(t.role) && s.Decide(permGrantElevated, uuid.Nil) == auth.Deny {
			return errForbidden
		}
		if n, err = revokeAccountSessions(ctx, q, staffID, reasonRevoked, now); err != nil || n == 0 {
			return err
		}
		return audit.Write(ctx, actorOf(s, by).entry(actionSessionsRevoked, staffID, nil, map[string]any{"count": n}, m))
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// RolesMatrix — роли по старшинству и права каталога без черновых строк в порядке каталога, у каждой
// роли — значение матрицы (Grant.String(): allow | scoped | dual | deny) — getStaffRoles.
func (a *App) RolesMatrix() staff.RolesView {
	v := staff.RolesView{Roles: make([]string, 0, len(perm.Roles))}
	for _, r := range perm.Roles {
		v.Roles = append(v.Roles, string(r))
	}
	for _, p := range perm.Catalog {
		if p.Draft {
			continue
		}
		g := make(map[string]string, len(perm.Roles))
		for _, r := range perm.Roles {
			g[string(r)] = perm.Matrix[p.Key][r].String()
		}
		v.Permissions = append(v.Permissions, staff.RolePermission{Key: p.Key, Mutating: p.Mutating, Grants: g})
	}
	return v
}

// likePrefix — шаблон ILIKE «начинается с q» (ESCAPE '\'): \, % и _ из ввода — буквально; пустой q
// (после обрезки пробелов) — без фильтра.
func likePrefix(q string) *string {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	p := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	return &p
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// Label — подпись сотрудника s для решений подтверждений вдвоём (снимок имени в approvals и журнале;
// ручки approve, reject и cancel в httpapi/admin) с перепроверкой без замка, как checkActor: учётки нет, она
// не active или закрыта, сессия погашена, истекла или не active — auth.ErrSessionInvalid (401 со стиранием
// cookie); заменена новой — auth.ErrSessionReplaced (401 без стирания: в браузере уже свежая); роль
// сменилась после загрузки сессии — 403. Подтверждение исполнитель перепроверяет ещё раз под замком
// (execLocks); отклонение и отмена исполнителя не зовут — для них это единственная проверка решившего
// (окно — между этим чтением и замком строки запроса).
func (a *App) Label(ctx context.Context, s *auth.Staff) (string, error) {
	q := staffdb.New(a.pool)
	r, err := q.ManageGetAccount(ctx, s.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", auth.ErrSessionInvalid
	}
	if err != nil {
		return "", err
	}
	acc := acct{id: r.ID, status: r.Status, role: r.Role, name: r.DisplayName, email: r.Email,
		version: r.Version, lockedUntil: r.LockedUntil}
	by, err := checkActor(ctx, q, s, map[uuid.UUID]acct{s.StaffID: acc}, a.clock.Now())
	if err != nil {
		return "", err
	}
	return label(by.name), nil
}
