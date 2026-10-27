package app

import (
	"context"
	"errors"
	"net/netip"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/token"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// LoadStaff — сотрудник по токену cookie (auth.StaffSessions, §4.7). Один запрос к базе на запрос,
// положительного кэша нет.
//   - нет сессии, погашена, учётка выключена или закрыта — auth.ErrSessionInvalid;
//     погашена как replaced меньше минуты назад — auth.ErrSessionReplaced (гонка смены токена);
//   - абсолютный срок истёк (8 ч; mfa_pending — 5 мин) — сессия гасится (expired), ErrSessionInvalid;
//   - стадия active: бездействие больше 20 минут или IP вне прежнего /24 (IPv4) или /48 (IPv6) —
//     Reauth; в setup и mfa_pending Reauth не ставится (P11);
//   - last_seen_at и ip обновляются не чаще раза в минуту, не на фоновых запросах (D10) и не в
//     Reauth — их меняет только Unlock (P11).
//
// Права — perm.Grants(роль) при загрузке, города модератора — из staff_role_cities.
func (a *App) LoadStaff(ctx context.Context, raw string, ip netip.Addr, background bool) (*auth.Staff, error) {
	if raw == "" {
		return nil, auth.ErrSessionInvalid
	}
	q := staffdb.New(a.pool)
	row, err := q.LoadSession(ctx, token.HashRefresh(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	now := a.clock.Now()
	if row.RevokedAt != nil {
		if row.RevokedReason != nil && *row.RevokedReason == reasonReplaced && now.Sub(*row.RevokedAt) < replacedGrace {
			return nil, auth.ErrSessionReplaced
		}
		return nil, auth.ErrSessionInvalid
	}
	if !now.Before(row.ExpiresAt) {
		if _, err := q.RevokeSession(ctx, staffdb.RevokeSessionParams{Now: now, Reason: reasonExpired, ID: row.ID}); err != nil {
			return nil, err
		}
		return nil, auth.ErrSessionInvalid
	}
	if row.Status == statusDisabled || row.LockedUntil != nil && now.Before(*row.LockedUntil) {
		return nil, auth.ErrSessionInvalid
	}
	st := &auth.Staff{
		StaffID: row.StaffID, SessionID: row.ID, Role: row.Role, Stage: row.Stage,
		Grants: perm.Grants(perm.Role(row.Role)),
	}
	if row.Role == auth.RoleModerator {
		st.CityIDs = row.CityIds
	}
	if row.Stage == auth.StageActive {
		st.Reauth = now.Sub(row.LastSeenAt) > idleTimeout || !samePrefix(row.Ip, ip)
	}
	if !st.Reauth && !background && now.Sub(row.LastSeenAt) >= touchEvery {
		seen := ip
		if !seen.IsValid() {
			seen = row.Ip
		}
		if err := q.TouchSession(ctx, staffdb.TouchSessionParams{
			Now: now, Ip: seen.Unmap(), ID: row.ID, SeenBefore: now.Add(-touchEvery),
		}); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// samePrefix — адреса в одной сети: /24 у IPv4, /48 у IPv6 (IPv4 в IPv6 — как IPv4). Разные семейства
// или неизвестный адрес — разные сети.
func samePrefix(a, b netip.Addr) bool {
	a, b = a.Unmap(), b.Unmap()
	if !a.IsValid() || !b.IsValid() || a.Is4() != b.Is4() {
		return false
	}
	bits := 48
	if a.Is4() {
		bits = 24
	}
	pa, err := a.Prefix(bits)
	if err != nil {
		return false
	}
	return pa.Contains(b)
}
