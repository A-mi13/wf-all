package app

import (
	"context"
	"fmt"

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

// nicknameVerdict — ник после NFKC и причина недоступности ("" — свободен). Проверка в базе — запросом q (пул или
// tx вызывающего); now — граница живого карантина.
func (a *App) nicknameVerdict(ctx context.Context, q *identitydb.Queries, nick string) (string, string, error) {
	n, problem := domain.CheckNickname(nick)
	if problem != domain.NicknameOK {
		return "", NickInvalid, nil
	}
	st, err := q.NicknameState(ctx, identitydb.NicknameStateParams{Nickname: n, Now: a.clock.Now()})
	if err != nil {
		return "", "", fmt.Errorf("identity: проверка ника: %w", err)
	}
	switch {
	case int(st.NormLen) < domain.NicknameMinLen:
		return "", NickInvalid, nil
	case st.Reserved:
		return n, NickReserved, nil
	case st.Taken || st.Quarantined:
		return n, NickTaken, nil
	}
	return n, "", nil
}

// CheckNickname — доступность ника (§5.8): reason — invalid | taken | reserved; свободен — available и "".
func (a *App) CheckNickname(ctx context.Context, nick string) (available bool, reason string, err error) {
	_, reason, err = a.nicknameVerdict(ctx, identitydb.New(a.pool), nick)
	if err != nil {
		return false, "", err
	}
	return reason == "", reason, nil
}
