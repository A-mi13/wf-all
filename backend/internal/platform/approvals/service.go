package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/approvals/approvalsdb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/peer"
)

const (
	// commentMax — длина комментария решения (ApprovalApprove/ApprovalReject контракта: maxLength 500).
	commentMax = 500
	// labelMax — снимок имени (CHECK approvals_*_label_check: 1–120 без крайних пробелов).
	labelMax = 120
	// errorMax — потолок текста error у failed, если ошибка исполнителя без кода.
	errorMax = 200

	auditPrefix = "platform.approval_"
)

// batchSize — порция ExpireDue и PurgeDecided: короткие транзакции и замки.
var batchSize = 100

// Service — запросы на подтверждение: создание (в транзакции модуля), решения, списки, чистка.
type Service struct {
	pool  *pgxpool.Pool
	clock clock.Clock
	reg   *Registry
}

// New — сервис подтверждений поверх реестра действий. Проверку инициатора реестр может получить и
// после New (сборка 10c: New, затем регистрация модулей) — до неё Approve не исполняет ничего.
func New(pool *pgxpool.Pool, c clock.Clock, reg *Registry) *Service {
	return &Service{pool: pool, clock: c, reg: reg}
}

// Decided — payload события platform.approval_decided: только id и коды, без имён и payload запроса.
type Decided struct {
	Action      string     `json:"action"`
	Status      string     `json:"status"`
	TargetID    *uuid.UUID `json:"target_id,omitempty"`
	ScopeCityID *uuid.UUID `json:"scope_city_id,omitempty"`
	RequestedBy uuid.UUID  `json:"requested_by"`
	DecidedBy   *uuid.UUID `json:"decided_by,omitempty"` // nil — решила система
}

// Request — запрос на подтверждение в транзакции модуля tx (ctx — из её db.InTx: аудит пишется
// туда же). Действие зарегистрировано; payload — JSON-объект с целым v ≥ 1; право — Permit(payload)
// (или Action.Permission) из каталога; инициатор: Decide(право, город цели) != Deny, иначе
// ErrForbidden. Срок — now + TTL, byLabel — снимок имени инициатора. Аудит — без payload (в нём
// свободный текст).
func (s *Service) Request(ctx context.Context, tx pgx.Tx, by *auth.Staff, byLabel, action string, payload any) (Approval, error) {
	a, ok := s.reg.action(action)
	if !ok {
		return Approval{}, fmt.Errorf("approvals: действие %q не зарегистрировано", action)
	}
	raw, err := encodePayload(payload)
	if err != nil {
		return Approval{}, fmt.Errorf("approvals: %s: %w", action, err)
	}
	permission := a.Permission
	if a.Permit != nil {
		if permission, err = a.Permit(raw); err != nil {
			return Approval{}, fmt.Errorf("approvals: %s: право запроса: %w", action, err)
		}
	}
	if err := checkPermission(permission); err != nil {
		return Approval{}, fmt.Errorf("approvals: %s: %w", action, err)
	}
	var targetID, cityID uuid.UUID
	if a.Target != nil {
		targetID, cityID = a.Target(raw)
	}
	if by == nil || by.Decide(permission, cityID) == auth.Deny {
		return Approval{}, ErrForbidden
	}
	label, err := snapshot(byLabel)
	if err != nil {
		return Approval{}, err
	}
	now := s.now()
	row, err := approvalsdb.New(tx).InsertApproval(ctx, approvalsdb.InsertApprovalParams{
		ID: id.New(), Action: action, Permission: permission, Payload: raw,
		TargetID: optUUID(targetID), ScopeCityID: optUUID(cityID),
		RequestedBy: by.StaffID, RequestedByLabel: label, RequestedAt: now, ExpiresAt: now.Add(TTL),
	})
	if err != nil {
		return Approval{}, fmt.Errorf("approvals: запрос %s: %w", action, err)
	}
	if err := audit.Write(ctx, audit.Entry{
		ActorStaffID: by.StaffID, ActorRole: by.Role, ActorLabel: label,
		Action: auditPrefix + "requested", ObjectType: AggregateApproval, ObjectID: row.ID, ScopeCityID: cityID,
		After: map[string]any{"action": action, "permission": permission, "target_id": optUUID(targetID),
			"expires_at": row.ExpiresAt},
		IP: peer.From(ctx).IP,
	}); err != nil {
		return Approval{}, err
	}
	return toApproval(row), nil
}

// Approve — подтверждение (D17, P13) одной транзакцией. Сессия active без Reauth; строка под
// замком; нет — ErrNotFound; не pending — ErrClosed; срок вышел — expired от системы коммитится и
// ErrClosed (D8); подтверждающий — инициатор или цель — ErrSelf; право — сохранённое право запроса
// в его городе, Deny — ErrForbidden; проверка инициатора: отказ — failed (initiator_changed)
// коммитится и ErrStale; исполнитель под savepoint: Fail(err) или ErrStale — изменения исполнителя
// откатываются, failed с кодом ошибки коммитится, наружу — исходная ошибка; иная ошибка или паника —
// откат всего (запрос остаётся pending). Успех — executed. При failed и expired возвращается и
// запрос в итоговом состоянии, и ошибка.
func (s *Service) Approve(ctx context.Context, st *auth.Staff, label string, apID uuid.UUID, comment string) (Approval, error) {
	if !active(st) {
		return Approval{}, ErrForbidden
	}
	lbl, err := snapshot(label)
	if err != nil {
		return Approval{}, err
	}
	cm, err := optionalComment(comment)
	if err != nil {
		return Approval{}, err
	}
	var out Approval
	var bizErr error
	err = db.InTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, closed, err := s.lockForDecision(ctx, tx, st, apID)
		if err != nil {
			return err
		}
		if closed != nil {
			out, bizErr = *closed, ErrClosed
			return nil
		}
		check := s.reg.initiatorCheck()
		if check == nil {
			return errNoInitiatorCheck
		}
		a, ok := s.reg.action(row.Action)
		if !ok {
			return fmt.Errorf("approvals: действие %q запроса %s не зарегистрировано", row.Action, row.ID)
		}
		decider := &actor{staff: st, label: lbl}
		if err := check(ctx, tx, row.RequestedBy, row.Permission); err != nil {
			if business(err) == nil {
				return fmt.Errorf("approvals: проверка инициатора: %w", err)
			}
			bizErr = ErrStale
			out, err = s.decide(ctx, tx, row, StatusFailed, decider, cm, ptr(ErrorInitiatorChanged))
			return err
		}
		sp, err := tx.Begin(ctx) // SAVEPOINT: отказ исполнителя не оставляет его частичных изменений
		if err != nil {
			return err
		}
		if execErr := a.Exec(ctx, sp, json.RawMessage(row.Payload), st); execErr != nil {
			visible := business(execErr)
			if visible == nil {
				return execErr
			}
			if err := sp.Rollback(ctx); err != nil {
				return err
			}
			bizErr = visible
			out, err = s.decide(ctx, tx, row, StatusFailed, decider, cm, ptr(errorText(visible)))
			return err
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		out, err = s.decide(ctx, tx, row, StatusExecuted, decider, cm, nil)
		return err
	})
	if err != nil {
		return Approval{}, err
	}
	return out, bizErr
}

// Reject — отклонение: проверки Approve без проверки инициатора и исполнителя; комментарий
// обязателен (400 validation.failed по body.comment).
func (s *Service) Reject(ctx context.Context, st *auth.Staff, label string, apID uuid.UUID, comment string) (Approval, error) {
	if !active(st) {
		return Approval{}, ErrForbidden
	}
	lbl, err := snapshot(label)
	if err != nil {
		return Approval{}, err
	}
	cm, err := optionalComment(comment)
	if err != nil {
		return Approval{}, err
	}
	if cm == nil {
		return Approval{}, httpx.NewFieldError("body.comment", "required")
	}
	var out Approval
	var bizErr error
	err = db.InTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, closed, err := s.lockForDecision(ctx, tx, st, apID)
		if err != nil {
			return err
		}
		if closed != nil {
			out, bizErr = *closed, ErrClosed
			return nil
		}
		out, err = s.decide(ctx, tx, row, StatusRejected, &actor{staff: st, label: lbl}, cm, nil)
		return err
	})
	if err != nil {
		return Approval{}, err
	}
	return out, bizErr
}

// Cancel — отмена инициатором ожидающего запроса. Чужой запрос (в том числе о себе) —
// ErrNotFound: существование чужих запросов не раскрывается (контракт cancelApproval); решённый —
// ErrClosed; просроченный — expired от системы коммитится и ErrClosed. label — снимок имени
// отменяющего (CHECK approvals_decider_label, ActorLabel аудита).
func (s *Service) Cancel(ctx context.Context, st *auth.Staff, label string, apID uuid.UUID) (Approval, error) {
	if !active(st) {
		return Approval{}, ErrForbidden
	}
	lbl, err := snapshot(label)
	if err != nil {
		return Approval{}, err
	}
	var out Approval
	var bizErr error
	err = db.InTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := approvalsdb.New(tx).LockApproval(ctx, apID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("approvals: запрос %s: %w", apID, err)
		case row.RequestedBy != st.StaffID:
			return ErrNotFound
		case row.Status != approvalsdb.ApprovalStatusPending:
			return ErrClosed
		case s.expired(row):
			bizErr = ErrClosed
			out, err = s.decide(ctx, tx, row, StatusExpired, nil, nil, nil)
			return err
		}
		out, err = s.decide(ctx, tx, row, StatusCancelled, &actor{staff: st, label: lbl}, nil, nil)
		return err
	})
	if err != nil {
		return Approval{}, err
	}
	return out, bizErr
}

// CancelByInitiator — в транзакции модуля (ctx — из её db.InTx) гасит все ожидающие запросы
// инициатора: cancelled от системы с причиной initiator_changed, аудит и событие на каждый. Зовёт
// staff при выключении, сбросе доступа и смене роли инициатора (D17). Запросы, где он цель, не
// гасятся — их отсекает проверка версии цели в исполнителе (ErrStale). Строку под замком
// параллельного решения пропускает (SKIP LOCKED): её доводит до конца это решение — его проверка
// инициатора увидит изменение; ожидание дало бы взаимную блокировку с путём «замок учётки → гашение».
func (s *Service) CancelByInitiator(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (int, error) {
	rows, err := approvalsdb.New(tx).CancelPendingByInitiator(ctx, approvalsdb.CancelPendingByInitiatorParams{
		Now: s.now(), RequestedBy: staffID,
	})
	if err != nil {
		return 0, fmt.Errorf("approvals: гашение запросов инициатора: %w", err)
	}
	for _, r := range rows {
		if err := s.record(ctx, r, nil); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

// ExpireDue — просроченные ожидающие (expires_at <= now) → expired от системы, с аудитом и
// событием; порциями по batchSize, каждая — своя транзакция. Строку под замком решения пропускает:
// решение само переводит просроченный запрос (Approve/Reject/Cancel проверяют срок под замком).
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	now := s.now()
	total := 0
	for {
		var n int
		err := db.InTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := approvalsdb.New(tx).ExpireDueBatch(ctx, approvalsdb.ExpireDueBatchParams{
				Now: now, Lim: int32(batchSize), //nolint:gosec // batchSize — 100 (в тестах меньше)
			})
			if err != nil {
				return err
			}
			for _, r := range rows {
				if err := s.record(ctx, r, nil); err != nil {
					return err
				}
			}
			n = len(rows)
			return nil
		})
		if err != nil {
			return total, fmt.Errorf("approvals: просрочка: %w", err)
		}
		total += n
		if n < batchSize {
			return total, nil
		}
	}
}

// PurgeDecided — удаляет решённые запросы с decided_at старше now − olderThan (P27: тикер чистки
// admin-api с DecidedRetention); ожидающие не трогает. olderThan ≤ 0 — ошибка: удалило бы всё
// решённое разом.
func (s *Service) PurgeDecided(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		return 0, fmt.Errorf("approvals: срок хранения решённых %v ≤ 0", olderThan)
	}
	before := s.now().Add(-olderThan)
	q := approvalsdb.New(s.pool)
	total := 0
	for {
		n, err := q.PurgeDecidedBatch(ctx, approvalsdb.PurgeDecidedBatchParams{
			Before: before, Lim: int32(batchSize), //nolint:gosec // batchSize — 100 (в тестах меньше)
		})
		if err != nil {
			return total, fmt.Errorf("approvals: чистка решённых: %w", err)
		}
		total += int(n)
		if n < int64(batchSize) {
			return total, nil
		}
	}
}

// lockForDecision — строка под замком для Approve и Reject и общие проверки решения. closed != nil —
// срок вышел: запрос переведён в expired (коммитится вызывающим), решать нечего.
func (s *Service) lockForDecision(ctx context.Context, tx pgx.Tx, st *auth.Staff, apID uuid.UUID) (approvalsdb.Approval, *Approval, error) {
	row, err := approvalsdb.New(tx).LockApproval(ctx, apID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return row, nil, ErrNotFound
	case err != nil:
		return row, nil, fmt.Errorf("approvals: запрос %s: %w", apID, err)
	case row.Status != approvalsdb.ApprovalStatusPending:
		return row, nil, ErrClosed
	case s.expired(row):
		ap, err := s.decide(ctx, tx, row, StatusExpired, nil, nil, nil)
		if err != nil {
			return row, nil, err
		}
		return row, &ap, nil
	case row.RequestedBy == st.StaffID || (row.TargetID != nil && *row.TargetID == st.StaffID):
		return row, nil, ErrSelf
	case st.Decide(row.Permission, val(row.ScopeCityID)) == auth.Deny:
		// право — сохранённое при запросе, не пересчёт Permit по payload
		return row, nil, ErrForbidden
	}
	return row, nil, nil
}

// actor — решивший человек; nil — система (просрочка, гашение).
type actor struct {
	staff *auth.Staff
	label string
}

// decide — переход ожидающего запроса в status с аудитом и событием в транзакции tx (ctx — её).
func (s *Service) decide(ctx context.Context, tx pgx.Tx, row approvalsdb.Approval, status string, by *actor,
	comment, errText *string) (Approval, error) {
	arg := approvalsdb.DecideApprovalParams{ID: row.ID, Status: status, DecidedByLabel: SystemLabel,
		DecidedAt: s.now(), DecisionComment: comment, Error: errText}
	if by != nil {
		arg.DecidedBy, arg.DecidedByLabel = &by.staff.StaffID, by.label
	}
	upd, err := approvalsdb.New(tx).DecideApproval(ctx, arg)
	if err != nil {
		return Approval{}, fmt.Errorf("approvals: решение %s → %s: %w", row.ID, status, err)
	}
	if err := s.record(ctx, upd, by); err != nil {
		return Approval{}, err
	}
	return toApproval(upd), nil
}

// record — аудит platform.approval_<status> (актор — решивший или system) и событие
// platform.approval_decided решённого запроса; ctx — транзакция решения.
func (s *Service) record(ctx context.Context, r approvalsdb.Approval, by *actor) error {
	status := string(r.Status)
	e := audit.Entry{ActorRole: SystemLabel, ActorLabel: SystemLabel,
		Action: auditPrefix + status, ObjectType: AggregateApproval, ObjectID: r.ID, ScopeCityID: val(r.ScopeCityID),
		After: map[string]any{"action": r.Action, "permission": r.Permission, "status": status,
			"target_id": r.TargetID, "error_code": r.Error},
		Reason: deref(r.DecisionComment),
	}
	if by != nil {
		e.ActorStaffID, e.ActorRole, e.ActorLabel, e.IP = by.staff.StaffID, by.staff.Role, by.label, peer.From(ctx).IP
	}
	if err := audit.Write(ctx, e); err != nil {
		return fmt.Errorf("approvals: аудит %s: %w", e.Action, err)
	}
	if _, err := events.Publish(ctx, events.Event{
		Type: EventDecided, SchemaVersion: 1,
		AggregateType: AggregateApproval, AggregateID: r.ID, AggregateVersion: 1,
		Payload: Decided{Action: r.Action, Status: status, TargetID: r.TargetID, ScopeCityID: r.ScopeCityID,
			RequestedBy: r.RequestedBy, DecidedBy: r.DecidedBy},
	}); err != nil {
		return fmt.Errorf("approvals: событие решения: %w", err)
	}
	return nil
}

// expired — срок вышел: now >= expires_at (ровно в срок — уже нельзя).
func (s *Service) expired(r approvalsdb.Approval) bool {
	return !s.now().Before(r.ExpiresAt)
}

// now — время часов с точностью базы (микросекунды): запрос, вернувшийся из Request, совпадает со
// строкой и курсором.
func (s *Service) now() time.Time {
	return s.clock.Now().UTC().Truncate(time.Microsecond)
}

func active(st *auth.Staff) bool {
	return st != nil && st.Stage == auth.StageActive && !st.Reauth
}

// encodePayload — payload запроса: JSON-объект с целым v ≥ 1 (CHECK approvals_payload_versioned
// проверяет только «число» — здесь строже, до SQL).
func encodePayload(p any) ([]byte, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("payload не сериализуется: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errors.New("payload — не JSON-объект")
	}
	v, ok := obj["v"]
	if !ok {
		return nil, errors.New("payload без версии v")
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		return nil, errors.New("версия payload v — не число")
	}
	if i, err := n.Int64(); err != nil || i < 1 {
		return nil, fmt.Errorf("версия payload v = %s — нужно целое ≥ 1", n)
	}
	return raw, nil
}

// snapshot — снимок имени для строки и аудита: без крайних пробелов, 1–120 символов.
func snapshot(label string) (string, error) {
	l := strings.TrimSpace(label)
	if n := utf8.RuneCountInString(l); n == 0 || n > labelMax {
		return "", fmt.Errorf("approvals: снимок имени %d символов — нужно 1–%d", n, labelMax)
	}
	return l, nil
}

// optionalComment — комментарий решения без крайних пробелов; пустой — nil; длиннее 500 — 400.
func optionalComment(c string) (*string, error) {
	c = strings.TrimSpace(c)
	if c == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(c) > commentMax {
		return nil, httpx.NewFieldError("body.comment", "maxLength")
	}
	return &c, nil
}

// coder — бизнес-ошибка модуля с кодом клиента (staff.Error).
type coder interface {
	error
	Code() string
}

// errorText — текст error у failed: код клиента (ProblemError или Code()), иначе текст ошибки с
// потолком длины — модульные ошибки исполнителей несут код, а не свободный текст.
func errorText(err error) string {
	if pe, ok := errors.AsType[httpx.ProblemError](err); ok {
		return pe.Problem().Code
	}
	if c, ok := errors.AsType[coder](err); ok {
		return c.Code()
	}
	t := err.Error()
	if utf8.RuneCountInString(t) > errorMax {
		t = string([]rune(t)[:errorMax])
	}
	return t
}

func toApproval(r approvalsdb.Approval) Approval {
	return Approval{
		ID: r.ID, Action: r.Action, Permission: r.Permission, Status: string(r.Status),
		Payload: json.RawMessage(r.Payload), TargetID: val(r.TargetID), ScopeCityID: val(r.ScopeCityID),
		RequestedBy: r.RequestedBy, RequestedByLabel: r.RequestedByLabel,
		RequestedAt: r.RequestedAt.UTC(), ExpiresAt: r.ExpiresAt.UTC(),
		DecidedBy: val(r.DecidedBy), DecidedByLabel: deref(r.DecidedByLabel), DecidedAt: utc(r.DecidedAt),
		Comment: deref(r.DecisionComment), Error: deref(r.Error),
	}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func optUUID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}

func val(u *uuid.UUID) uuid.UUID {
	if u == nil {
		return uuid.Nil
	}
	return *u
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptr(s string) *string { return &s }
