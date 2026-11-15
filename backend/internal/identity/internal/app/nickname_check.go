package app

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
)

// Доступность ника (спека §5.8) — владелец Task 10: формат (domain.CheckNickname), длина после normalize_text не
// меньше 3 (двойники латиница/кириллица и знаки уходят, 李小龍 и Ἀλέξης нормализуются в пустую строку), резерв,
// занятость живым аккаунтом и живой карантин чужого ника.

// Причины недоступности ника (NicknameUnavailableReason контракта).
const (
	NickInvalid  = "invalid"
	NickTaken    = "taken"
	NickReserved = "reserved"
)

// nickCheck — вердикт ника: Nick — после NFKC, Normalized — normalize_text (ключ занятости и карантина), Reason —
// причина недоступности ("" — свободен).
type nickCheck struct {
	Nick, Normalized, Reason string
}

// nicknameCheck — доступность ника одним запросом NicknameState (регистрация, проверка и смена ника). owner — владелец
// (смена ника): его аккаунт и его карантин ник не занимают; nil — не исключается никто. Запрос — q (пул или tx
// вызывающего); now — граница живого карантина.
func (a *App) nicknameCheck(ctx context.Context, q *identitydb.Queries, nick string, owner *uuid.UUID) (nickCheck, error) {
	n, problem := domain.CheckNickname(nick)
	if problem != domain.NicknameOK {
		return nickCheck{Reason: NickInvalid}, nil
	}
	st, err := q.NicknameState(ctx, identitydb.NicknameStateParams{Nickname: n, UserID: owner, Now: a.clock.Now()})
	if err != nil {
		return nickCheck{}, fmt.Errorf("identity: проверка ника: %w", err)
	}
	c := nickCheck{Nick: n, Normalized: st.Normalized}
	switch {
	case utf8.RuneCountInString(st.Normalized) < domain.NicknameMinLen:
		return nickCheck{Reason: NickInvalid}, nil
	case st.Reserved:
		c.Reason = NickReserved
	case st.Taken || st.Quarantined:
		c.Reason = NickTaken
	}
	return c, nil
}

// nicknameVerdict — ник после NFKC и причина недоступности ("" — свободен) для регистрации: никто не исключается.
func (a *App) nicknameVerdict(ctx context.Context, q *identitydb.Queries, nick string) (string, string, error) {
	c, err := a.nicknameCheck(ctx, q, nick, nil)
	return c.Nick, c.Reason, err
}

// CheckNickname — доступность ника (§5.8): reason — invalid | taken | reserved; свободен — available и "".
func (a *App) CheckNickname(ctx context.Context, nick string) (available bool, reason string, err error) {
	_, reason, err = a.nicknameVerdict(ctx, identitydb.New(a.pool), nick)
	if err != nil {
		return false, "", err
	}
	return reason == "", reason, nil
}
