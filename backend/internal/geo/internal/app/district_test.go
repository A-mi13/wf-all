package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/perm"
)

// Районы (Task 12): база на тест под ролью admin (actEnv), города сида stavropol и mihaylovsk.

func createDistrict(a *app.Admin, cityID uuid.UUID, name string) (geo.AdminDistrict, error) {
	return a.CreateDistrict(context.Background(), staffAs(perm.Admin), cityID, name, approvals.Meta{})
}

func updateDistrict(a *app.Admin, cityID, id uuid.UUID, p app.DistrictPatch) (geo.AdminDistrict, error) {
	return a.UpdateDistrict(context.Background(), staffAs(perm.Admin), cityID, id, p, approvals.Meta{})
}

func boolp(b bool) *bool { return &b }

// Создание: название очищено, версия 1, время — часов сценария; событие geo.district_created (агрегат district,
// версия 1) и аудит geo.district_created с городом записи — в той же транзакции. Нет города — 404; негодное имя — 400.
func TestCreateDistrict(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	s := staffAs(perm.Admin)
	d, err := e.admin().CreateDistrict(context.Background(), s, stav, "  Промышленный   район ", approvals.Meta{})
	if err != nil || d.Name != "Промышленный район" || d.Version != 1 || d.ArchivedAt != nil {
		t.Fatalf("%+v %v", d, err)
	}
	if n := e.count(t, `SELECT count(*) FROM districts WHERE id = $1 AND city_id = $2 AND created_at = $3`, d.ID, stav, tAct); n != 1 {
		t.Fatal("строка района")
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.district_created' AND aggregate_type = 'district'
		AND aggregate_id = $1 AND aggregate_version = 1 AND payload->>'district_id' = $2 AND payload->>'city_id' = $3`,
		d.ID, d.ID.String(), stav.String()); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.district_created' AND actor_staff_id = $1
		AND object_type = 'district' AND object_id = $2 AND scope_city_id = $3 AND after->>'name' = 'Промышленный район'`,
		s.StaffID, d.ID, stav); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
	if _, err := createDistrict(e.admin(), uuid.New(), "Центр"); !errors.Is(err, geo.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
	_, err = createDistrict(e.admin(), stav, " \t ")
	wantField(t, err, "body.name", "minLength")
	_, err = createDistrict(e.admin(), stav, "Центр"+string(rune(0x200b)))
	wantField(t, err, "body.name", "pattern")
}

// Дубль среди действующих районов города — без учёта регистра и ё/е (normalize_text): 409; в другом городе — можно.
func TestDistrictNameTaken(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	a := e.admin()
	if _, err := createDistrict(a, stav, "Тёплый Стан"); err != nil {
		t.Fatal(err)
	}
	if _, err := createDistrict(a, stav, "теплый стан"); !errors.Is(err, geo.ErrDistrictNameTaken) {
		t.Fatalf("дубль: %v", err)
	}
	if _, err := createDistrict(a, mih, "Тёплый Стан"); err != nil {
		t.Fatalf("другой город: %v", err)
	}
	other, err := createDistrict(a, stav, "Центр")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updateDistrict(a, stav, other.ID, app.DistrictPatch{Name: strp("ТЕПЛЫЙ СТАН")}); !errors.Is(err, geo.ErrDistrictNameTaken) {
		t.Fatalf("переименование в занятое: %v", err)
	}
}

// Архивный район не мешает новому с тем же именем; архивирование — время часов, событие archived; возврат из архива
// при занятом имени — 409, ничего не записано.
func TestArchiveFreesName(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	d, err := createDistrict(a, stav, "Центр")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(time.Hour)
	arch, err := updateDistrict(a, stav, d.ID, app.DistrictPatch{Archived: boolp(true)})
	if err != nil || arch.ArchivedAt == nil || !arch.ArchivedAt.Equal(tAct.Add(time.Hour)) || arch.Version != 2 {
		t.Fatalf("архив: %+v %v", arch, err)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.district_archived' AND aggregate_id = $1
		AND aggregate_version = 2`, d.ID); n != 1 {
		t.Fatalf("событие архива: %d", n)
	}
	if _, err := createDistrict(a, stav, "центр"); err != nil {
		t.Fatalf("имя архивного занято: %v", err)
	}
	// публичная карточка города не показывает архивный
	card, err := geo.New(e.as).City(context.Background(), stav, "ru")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range card.Districts {
		if x.ID == d.ID {
			t.Fatal("архивный район в публичной карточке")
		}
	}
}

func TestUnarchiveNameTaken(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	d, err := createDistrict(a, stav, "Центр")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updateDistrict(a, stav, d.ID, app.DistrictPatch{Archived: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := createDistrict(a, stav, "Центр"); err != nil {
		t.Fatal(err)
	}
	if _, err := updateDistrict(a, stav, d.ID, app.DistrictPatch{Archived: boolp(false)}); !errors.Is(err, geo.ErrDistrictNameTaken) {
		t.Fatalf("возврат из архива: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM districts WHERE id = $1 AND archived_at IS NOT NULL AND version = 2`, d.ID); n != 1 {
		t.Fatal("записано при отказе")
	}
	// возврат с новым свободным именем — можно; событие updated, аудит с изменёнными полями
	back, err := updateDistrict(a, stav, d.ID, app.DistrictPatch{Archived: boolp(false), Name: strp("Старый центр")})
	if err != nil || back.ArchivedAt != nil || back.Name != "Старый центр" || back.Version != 3 {
		t.Fatalf("%+v %v", back, err)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE event_type = 'geo.district_updated' AND aggregate_id = $1
		AND aggregate_version = 3`, d.ID); n != 1 {
		t.Fatalf("событие: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.district_updated' AND object_id = $1
		AND before = '{"name":"Центр","archived":true}'::jsonb AND after = '{"name":"Старый центр","archived":false}'::jsonb`,
		d.ID); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
}

// Спека §6.1: район чужого города по своему cityId в пути — 404 geo.district_not_found (существование не
// раскрывается), ничего не записано; модератор своего города — тоже.
func TestDistrictOfOtherCity(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	foreign, err := createDistrict(e.admin(), mih, "Центр")
	if err != nil {
		t.Fatal(err)
	}
	mod := staffAs(perm.Moderator, stav)
	_, err = e.admin().UpdateDistrict(context.Background(), mod, stav, foreign.ID, app.DistrictPatch{Archived: boolp(true)}, approvals.Meta{})
	if !errors.Is(err, geo.ErrDistrictNotFound) {
		t.Fatalf("район чужого города: %v", err)
	}
	if _, err := updateDistrict(e.admin(), stav, uuid.New(), app.DistrictPatch{Name: strp("X")}); !errors.Is(err, geo.ErrDistrictNotFound) {
		t.Fatalf("нет района: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM districts WHERE id = $1 AND archived_at IS NULL AND version = 1`, foreign.ID); n != 1 {
		t.Fatal("чужой район изменён")
	}
}

// Модератор чужого города (сценарий — защита в глубину за конвейером) — 403; своего — можно; поддержка — 403.
func TestDistrictModeratorScope(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav, mih := e.cityID(t, "stavropol"), e.cityID(t, "mihaylovsk")
	ctx := context.Background()
	_, err := e.admin().CreateDistrict(ctx, staffAs(perm.Moderator, mih), stav, "Центр", approvals.Meta{})
	wantForbidden(t, err)
	_, err = e.admin().CreateDistrict(ctx, staffAs(perm.Support), stav, "Центр", approvals.Meta{})
	wantForbidden(t, err)
	if _, err := e.admin().CreateDistrict(ctx, staffAs(perm.Moderator, stav), stav, "Центр", approvals.Meta{}); err != nil {
		t.Fatalf("модератор своего: %v", err)
	}
}

// Ничего не изменилось (то же имя, архивирование архивного) — без события, аудита и роста версии; пустой patch — 400.
func TestUpdateDistrictNoChange(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	a := e.admin()
	d, err := createDistrict(a, stav, "Центр")
	if err != nil {
		t.Fatal(err)
	}
	got, err := updateDistrict(a, stav, d.ID, app.DistrictPatch{Name: strp(" Центр "), Archived: boolp(false)})
	if err != nil || got.Version != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if n := e.count(t, `SELECT (SELECT count(*) FROM outbox WHERE aggregate_id = $1) + (SELECT count(*) FROM audit_log WHERE object_id = $1)`, d.ID); n != 2 {
		t.Fatalf("записи без изменений: %d (ждали только создание)", n)
	}
	_, err = updateDistrict(a, stav, d.ID, app.DistrictPatch{})
	wantField(t, err, "body", "minProperties")
}

// Два одновременных создания одноимённых районов (барьер: оба прошли проверки до вставки) — одно создано, другое —
// 409 geo.district_name_taken (23505 districts_city_name_active_key → код, не 500).
func TestDistrictConcurrentSameName(t *testing.T) {
	t.Parallel()
	e := newActEnv(t)
	stav := e.cityID(t, "stavropol")
	at, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	second := e.admin()
	second.SetStageHook(func(_ context.Context, s string) {
		if s == "district" {
			once.Do(func() { close(at); <-done })
		}
	})
	var errB error
	var wg sync.WaitGroup
	wg.Go(func() { _, errB = createDistrict(second, stav, "Центр") })
	<-at
	_, errA := createDistrict(e.admin(), stav, "ЦЕНТР")
	close(done)
	wg.Wait()
	if errA != nil || !errors.Is(errB, geo.ErrDistrictNameTaken) {
		t.Fatalf("a: %v, b: %v", errA, errB)
	}
	if n := e.count(t, `SELECT count(*) FROM districts WHERE city_id = $1`, stav); n != 1 {
		t.Fatalf("районов %d", n)
	}
}
