package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
)

// Поиск игрока по почте для поддержки и статистика способов входа (спека §3.15 п. 2, §7.1, §4.3; план I-4, Task 5,
// R10, R37). Роль admin — только чтение. Каждый поиск — в журнал с причиной, почты в записи нет никогда: найден —
// identity.pii_read по игроку (audit.WriteRead), не найден — identity.player_lookup без object_id. Модератору игрок
// чужого города и игрок без города неотличимы от «нет» — та же запись журнала и тот же 404.

// permPiiRead — право поиска игрока (allow — глобальные роли, scoped — модератор, только его города).
const permPiiRead = "identity.pii.read"

// PlayerLookupReset — назначенный сброс 2FA найденного игрока.
type PlayerLookupReset struct {
	ID        uuid.UUID
	ExecuteAt time.Time
}

// PlayerLookup — игрок, найденный по почте (IdentityPlayerLookup).
type PlayerLookup struct {
	ID         uuid.UUID
	Nickname   string
	CityID     uuid.UUID // всегда задан: игрок без города не находится
	Status     string
	CreatedAt  time.Time
	MfaEnabled bool
	MfaReset   *PlayerLookupReset
}

// AuthMethodUsage — строка статистики способов входа: у скольких способ есть и сколько входят только им.
type AuthMethodUsage struct {
	Method     string
	OnlyMethod int64
	Linked     int64
}

// authUsageMethods — строки статистики в порядке ответа (IdentityAuthMethodKind).
var authUsageMethods = []string{"apple", "google", "passkeys"}

// FindPlayerByEmail — findPlayerByEmail: email — уже каноническая форма emailaddr (decodeLookupEmail), reason —
// причина доступа (3–200 символов, в audit_log.reason). Право identity.pii.read: allow — любой игрок, scoped — только
// игрок города модератора (по GrantScoped, не по len(CityIDs): у модератора без городов — никто); иначе 403. Не найден
// (нет, deleted, без города, вне скоупа) — 404 user_not_found; запись журнала при этом коммитится (одна tx, ошибка — после неё).
func (a *App) FindPlayerByEmail(ctx context.Context, st *auth.Staff, email, reason string) (PlayerLookup, error) {
	if st == nil || st.Stage != auth.StageActive || st.Reauth {
		return PlayerLookup{}, errStaffForbidden
	}
	grant := st.Grant(permPiiRead)
	if grant != auth.GrantAllow && grant != auth.GrantScoped {
		return PlayerLookup{}, errStaffForbidden
	}
	m := approvals.MetaFrom(ctx)
	entry := audit.Entry{ActorStaffID: st.StaffID, ActorRole: st.Role, ActorLabel: st.Label, ObjectType: objectUser,
		Reason: reason, IP: m.IP, UserAgent: m.UserAgent}
	var (
		out   PlayerLookup
		found bool
	)
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, err := identitydb.New(tx).LookupPlayerByEmail(ctx, email)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("identity: поиск игрока по почте: %w", err)
		default:
			// игрок без города (город удалён, ON DELETE SET NULL) — как «нет» для всех: город в ответе обязателен, нулевой
			// uuid был бы ложными данными (решение контроллера 17.11); скоуп модератора — чужой город тоже как «нет»
			found = r.CityID != nil && (grant == auth.GrantAllow || st.Decide(permPiiRead, *r.CityID) == auth.Allow)
		}
		if !found {
			e := entry
			e.Action, e.After = ActionPlayerLookup, map[string]any{"found": false}
			return audit.WriteRead(ctx, tx, e)
		}
		out = PlayerLookup{ID: r.ID, Nickname: r.Nickname, CityID: *r.CityID, Status: r.Status,
			CreatedAt: r.CreatedAt, MfaEnabled: r.MfaEnabled}
		if r.ResetID != nil && r.ResetExecuteAt != nil {
			out.MfaReset = &PlayerLookupReset{ID: *r.ResetID, ExecuteAt: *r.ResetExecuteAt}
		}
		e := entry
		e.Action, e.ObjectID, e.ScopeCityID = ActionPiiRead, r.ID, out.CityID
		return audit.WriteRead(ctx, tx, e)
	})
	if err != nil {
		return PlayerLookup{}, err
	}
	if !found {
		return PlayerLookup{}, errUserNotFound
	}
	return out, nil
}

// AuthMethodsUsage — getAuthMethodsUsage: представление identity_read_auth_method_usage (R2); ровно три строки
// apple, google, passkeys — иначе представление разошлось с контрактом (ошибка, 500).
func (a *App) AuthMethodsUsage(ctx context.Context) ([]AuthMethodUsage, error) {
	rows, err := identitydb.New(a.pool).AuthMethodUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: статистика способов входа: %w", err)
	}
	out := make([]AuthMethodUsage, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuthMethodUsage{Method: r.Method, OnlyMethod: r.OnlyMethod, Linked: r.Linked})
	}
	if !slices.EqualFunc(out, authUsageMethods, func(u AuthMethodUsage, m string) bool { return u.Method == m }) {
		return nil, fmt.Errorf("identity: статистика способов входа: строки %v, ждали %v", out, authUsageMethods)
	}
	return out, nil
}
