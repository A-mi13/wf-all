package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Связь «сотрудник → учётка игрока» (спека identity §7.6; план I-1 R15, план I-2 R8, план I-4 Task 6, R14, R15).
// Только владелец (staff.player_links.manage); входа связь не даёт — модули отказывают в действиях над своей учёткой
// (identity.self_action). Изменение связи — изменение учётки: staff_accounts.version + 1 (R14) — InitiatorCheck
// отсекает запросы сотрудника, созданные до связи (и все его ожидающие запросы — принято).
//
// Порядок замков — как у управления (m7, M-4): lockAccounts (цель FOR UPDATE, автор FOR SHARE, по возрастанию id),
// checkActor под замком, затем строка staff_player_links цели. lockOwners не нужен: страж последнего владельца связь не
// затрагивает. Порт PlayerDirectory (identity) идёт своим соединением пула — его зовут до транзакции, не под замком:
// второе соединение под замком при исчерпанном пуле ждало бы вечно. Решение по заявке (identity, R38) держит FOR SHARE
// строки своего сотрудника (staff.LinkedPlayerTx) — с FOR UPDATE цели здесь сериализуется.

const (
	permPlayerLinks       = "staff.player_links.manage"
	actionPlayerLinked    = "staff.player_linked"
	actionPlayerUnlinked  = "staff.player_unlinked"
	playerLinksUserKey    = "staff_player_links_user_id_key" // UNIQUE (user_id): учётка игрока — у одного сотрудника
	playerLinkReread      = 5 * time.Second                  // срок сборки ответа после коммита (как committedAccount)
	playerLinkAuditUserID = "user_id"
	pgUniqueViolation     = "23505"
)

// errNoPlayers — сборка без порта PlayerDirectory (staff.Deps.Players): ошибка сборки, не ответ клиенту.
var errNoPlayers = errors.New("staff: нет порта учёток игроков (Deps.Players)")

// PutPlayerLink — putStaffPlayerLink: право staff.player_links.manage = allow (иначе 403); одна транзакция — замки
// учёток, автор перепроверен (не active, сессия погашена — 401; роль сменилась — 403), сотрудника нет — 404
// staff.not_found, игрока нет или удалён — 422 staff.player_not_found; та же связь — без изменений (без версии,
// аудита, письма); учётка игрока у другого сотрудника — 409 staff.player_link_taken (и 23505 при гонке); иначе
// вставка или замена, версия цели + 1, аудит staff.player_linked (after {user_id}, before — прежняя связь), письмо
// staff.player_linked сотруднику. Ответ собирается после коммита на ctx без отмены (обрыв клиента не даёт 500 при
// выполненном действии).
func (a *App) PutPlayerLink(ctx context.Context, by *auth.Staff, staffID, userID uuid.UUID, m staff.Meta) (staff.PlayerLink, error) {
	if by == nil || by.Decide(permPlayerLinks, uuid.Nil) != auth.Allow {
		return staff.PlayerLink{}, errForbidden
	}
	if a.players == nil {
		return staff.PlayerLink{}, errNoPlayers
	}
	_, exists, err := a.players.Player(ctx, userID)
	if err != nil {
		return staff.PlayerLink{}, fmt.Errorf("staff: учётка игрока: %w", err)
	}
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		locked, err := lockAccounts(ctx, q, staffID, by.StaffID)
		if err != nil {
			return err
		}
		actorAcct, err := checkActor(ctx, q, by, locked, a.clock.Now())
		if err != nil {
			return err
		}
		if _, ok := locked[staffID]; !ok {
			return staff.ErrNotFound
		}
		if !exists {
			return staff.ErrPlayerNotFound
		}
		cur, err := q.PlayerLinkOf(ctx, staffID)
		hasCur := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasCur && cur.UserID == userID {
			return nil // та же связь — без изменений
		}
		holder, err := q.PlayerLinkHolder(ctx, userID)
		switch {
		case err == nil && holder != staffID:
			return staff.ErrPlayerLinkTaken
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		now := a.clock.Now()
		if err := q.PlayerLinkPut(ctx, staffdb.PlayerLinkPutParams{StaffID: staffID, UserID: userID,
			CreatedBy: by.StaffID, CreatedAt: now}); err != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUniqueViolation &&
				pgErr.ConstraintName == playerLinksUserKey {
				return staff.ErrPlayerLinkTaken // параллельная связь той же учётки с другим сотрудником
			}
			return err
		}
		var before map[string]any
		if hasCur {
			before = map[string]any{playerLinkAuditUserID: cur.UserID}
		}
		return a.playerLinkChanged(ctx, tx, q, by, actorAcct, staffID, actionPlayerLinked, staff.MailPlayerLinked,
			before, map[string]any{playerLinkAuditUserID: userID}, m)
	})
	if err != nil {
		return staff.PlayerLink{}, err
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), playerLinkReread)
	defer cancel()
	link, ok, err := a.playerLinkView(rctx, staffdb.New(a.pool), staffID)
	if err != nil {
		return staff.PlayerLink{}, err
	}
	if !ok {
		return staff.PlayerLink{}, fmt.Errorf("staff: связь %s не найдена после коммита", staffID)
	}
	return link, nil
}

// DeletePlayerLink — deleteStaffPlayerLink: право, замки и автор — как PutPlayerLink; сотрудника нет — 404; связи нет
// — nil без записи и письма; иначе удаление, версия + 1, аудит staff.player_unlinked (before {user_id}), письмо
// staff.player_unlinked сотруднику.
func (a *App) DeletePlayerLink(ctx context.Context, by *auth.Staff, staffID uuid.UUID, m staff.Meta) error {
	if by == nil || by.Decide(permPlayerLinks, uuid.Nil) != auth.Allow {
		return errForbidden
	}
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		locked, err := lockAccounts(ctx, q, staffID, by.StaffID)
		if err != nil {
			return err
		}
		actorAcct, err := checkActor(ctx, q, by, locked, a.clock.Now())
		if err != nil {
			return err
		}
		if _, ok := locked[staffID]; !ok {
			return staff.ErrNotFound
		}
		gone, err := q.PlayerLinkDelete(ctx, staffID)
		if err != nil || len(gone) == 0 {
			return err
		}
		return a.playerLinkChanged(ctx, tx, q, by, actorAcct, staffID, actionPlayerUnlinked, staff.MailPlayerUnlinked,
			map[string]any{playerLinkAuditUserID: gone[0]}, nil, m)
	})
}

// playerLinkChanged — общий хвост изменения связи под замком цели: версия + 1 (R14), аудит (id записи — ref письма),
// письмо сотруднику.
func (a *App) playerLinkChanged(ctx context.Context, tx pgx.Tx, q *staffdb.Queries, by *auth.Staff, actorAcct acct,
	staffID uuid.UUID, action, kind string, before, after map[string]any, m staff.Meta) error {
	if _, err := q.ManageBumpVersion(ctx, staffID); err != nil {
		return err
	}
	e := actorOf(by, actorAcct).entry(action, staffID, before, after, m)
	e.ID = id.New()
	if err := audit.Write(ctx, e); err != nil {
		return err
	}
	_, err := a.river.InsertTx(ctx, tx, staff.NewMailArgs(kind, e.ID, staffID), nil)
	return err
}

// playerLinkView — связь сотрудника для карточки и ответа PUT: ник — через порт на каждое чтение (учётка удалена —
// ника нет, пустая строка: связь видна владельцу, чтобы её снять), подпись — текущее имя автора, без имени — почта.
// Связи нет — ok = false.
func (a *App) playerLinkView(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID) (staff.PlayerLink, bool, error) {
	r, err := q.PlayerLinkCard(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return staff.PlayerLink{}, false, nil
	}
	if err != nil {
		return staff.PlayerLink{}, false, err
	}
	if a.players == nil {
		return staff.PlayerLink{}, false, errNoPlayers
	}
	nick, _, err := a.players.Player(ctx, r.UserID)
	if err != nil {
		return staff.PlayerLink{}, false, fmt.Errorf("staff: ник игрока: %w", err)
	}
	byLabel := label(r.ByName)
	if byLabel == "" {
		byLabel = r.ByEmail
	}
	return staff.PlayerLink{UserID: r.UserID, Nickname: nick, LinkedAt: r.CreatedAt.UTC(), LinkedByLabel: byLabel}, true, nil
}

// ComposePlayerLink — письмо staff.player_linked | staff.player_unlinked получателю recipient: ref — запись аудита
// действия (кто — подпись автора записи, какой игрок — user_id из after или before, ник — через порт). mail.ErrSkip:
// записи нет, она другого действия или о другом сотруднике, получатель выключен, учётки игрока больше нет.
func (a *App) ComposePlayerLink(ctx context.Context, ref, recipient uuid.UUID, kind string) (mail.Message, error) {
	want := map[string]string{staff.MailPlayerLinked: actionPlayerLinked, staff.MailPlayerUnlinked: actionPlayerUnlinked}[kind]
	rec, ok, err := audit.Get(ctx, a.pool, ref)
	if err != nil {
		return mail.Message{}, err
	}
	if !ok || want == "" || rec.Action != want || rec.ObjectID != recipient {
		return mail.Message{}, mail.ErrSkip
	}
	raw := rec.After
	if kind == staff.MailPlayerUnlinked {
		raw = rec.Before
	}
	var link struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &link); err != nil || link.UserID == uuid.Nil {
		return mail.Message{}, fmt.Errorf("staff: запись %s без user_id: %w", ref, err)
	}
	q := staffdb.New(a.pool)
	r, err := q.GetMailRecipient(ctx, recipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return mail.Message{}, mail.ErrSkip
	}
	if err != nil {
		return mail.Message{}, err
	}
	if r.Status == statusDisabled {
		return mail.Message{}, mail.ErrSkip
	}
	who, err := q.ManageAccountMail(ctx, recipient)
	if err != nil {
		return mail.Message{}, err
	}
	if a.players == nil {
		return mail.Message{}, errNoPlayers
	}
	nick, exists, err := a.players.Player(ctx, link.UserID)
	if err != nil {
		return mail.Message{}, err
	}
	if !exists {
		return mail.Message{}, mail.ErrSkip
	}
	args := map[string]any{"name": strings.TrimSpace(who.DisplayName), "owner": rec.ActorLabel, "nickname": nick}
	return a.compose(mailTo{email: r.Email, locale: mailLocale(r.Locale)}, kind, args)
}
