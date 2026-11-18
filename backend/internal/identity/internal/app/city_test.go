package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/db"
)

func changeCity(e *env, uid, city uuid.UUID) error {
	return db.InTx(context.Background(), e.a.pool, func(ctx context.Context, tx pgx.Tx) error {
		return e.a.ChangeCityInTx(ctx, tx, uid, city)
	})
}

// §10.2: город сменён — users.city_id, версия + 1, событие city_changed (from/to), аудит; тот же город — ничего; нет
// города или страна выключена — ErrCityNotFound без записей; нет аккаунта и удалённый — свои ошибки.
func TestChangeCityInTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, cities := captaincyPlayer(t, e, "city_move")
	_, _, _, ver0 := userState(t, e, uid)
	if err := changeCity(e, uid, cities.main); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ver := userState(t, e, uid); ver != ver0 || count(t, e.owner, `SELECT count(*) FROM outbox`) != 0 {
		t.Fatal("тот же город что-то записал")
	}
	for name, c := range map[string]uuid.UUID{"выключенная страна": cities.disabled, "нет города": uuid.New()} {
		if err := changeCity(e, uid, c); !errors.Is(err, ErrCityNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := changeCity(e, uid, cities.other); err != nil {
		t.Fatal(err)
	}
	_, _, _, ver := userState(t, e, uid)
	if ver != ver0+1 || count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND city_id = $2`, uid, cities.other) != 1 {
		t.Fatalf("город не сменён: версия %d (было %d)", ver, ver0)
	}
	var raw []byte
	var ev int64
	if err := e.owner.QueryRow(context.Background(), `SELECT payload, aggregate_version FROM outbox WHERE event_type = $1
		AND aggregate_id = $2`, identity.EventCityChanged, uid).Scan(&raw, &ev); err != nil {
		t.Fatal(err)
	}
	var p identity.CityChangedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.UserID != uid || p.FromCityID != cities.main || p.ToCityID != cities.other || ev != ver {
		t.Fatalf("city_changed %+v v%d", p, ev)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2 AND actor_user_id = $2`,
		ActionCityChanged, uid); n != 1 {
		t.Fatal("аудит смены города")
	}
	if err := changeCity(e, uuid.New(), cities.main); !errors.Is(err, identity.ErrUserNotFound) {
		t.Fatalf("нет аккаунта: %v", err)
	}
	acctExec(t, e, `UPDATE users SET status = 'deleted' WHERE id = $1`, uid)
	if err := changeCity(e, uid, cities.main); !errors.Is(err, ErrUserDeleted) {
		t.Fatalf("удалённый: %v", err)
	}
}

// R39 (Q6, вариант (а)): смена города отзывает открытую заявку прежнего города — withdrawn с событием и письмом
// captain_application_withdrawn_city в той же tx; паузы нет — подача в новом городе сразу проходит; допуск прежнего
// города на месте.
func TestChangeCityWithdrawNoCooldown(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, cities := captaincyPlayer(t, e, "city_withdraw")
	ctx := context.Background()
	open, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{Note: ptr("играю по средам")})
	if err != nil {
		t.Fatal(err)
	}
	permitApp := grantPermit(t, e, uid, cities.disabled) // допуск другого города — не трогается
	if err := changeCity(e, uid, cities.other); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'withdrawn'
		AND closed_at = $2 AND version = 2 AND reviewed_at IS NULL`, open.ID, testNow); n != 1 {
		t.Fatal("заявка прежнего города не отозвана")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2
		AND aggregate_version = 2`, identity.EventCaptainApplicationWithdrawn, open.ID); n != 1 {
		t.Fatal("события отзыва нет")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1
		AND args->>'ref' = $2`, MailCaptainApplicationWithdrawnCity, open.ID.String()); n != 1 {
		t.Fatal("письма об отзыве нет")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE application_id = $1 AND revoked_at IS NULL`,
		permitApp); n != 1 {
		t.Fatal("допуск прежнего города снят")
	}
	got, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{Note: ptr("новый город")})
	if err != nil || got.CityID != cities.other {
		t.Fatalf("подача в новом городе после отзыва сменой города: %+v %v", got, err)
	}
	if c, err := e.a.MyCaptaincy(ctx, uid); err != nil || c.AvailableAt != nil {
		t.Fatalf("пауза после отзыва сменой города: %+v %v", c, err)
	}
}
