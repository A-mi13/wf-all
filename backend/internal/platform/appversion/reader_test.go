package appversion_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Чтение под ролью api (права прода из grants.sql): нет строки — ok=false, не ошибка.
func TestReaderGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	r := appversion.NewReader(pools.As)

	// миграции версий не заводят: до админки публичная ручка отдаёт null-поля (спека geo §1)
	for _, p := range []appversion.Platform{appversion.IOS, appversion.Android, "web"} {
		if v, ok, err := r.Get(ctx, p); err != nil || ok || v != (appversion.Versions{}) {
			t.Fatalf("%s до заведения: %+v %v %v", p, v, ok, err)
		}
	}

	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO app_versions
		(platform, min_version, recommended_version, store_url, version, updated_at)
		VALUES ('android', '1.2.3', '1.10.0', 'https://play.google.com/store/apps/details?id=app.wf', 3, $1)`, at); err != nil {
		t.Fatal(err)
	}
	got, ok, err := r.Get(ctx, appversion.Android)
	if err != nil || !ok || !got.UpdatedAt.Equal(at) {
		t.Fatalf("android: %+v %v %v", got, ok, err)
	}
	got.UpdatedAt = at // зона времени из pgx не важна, момент проверен выше
	want := appversion.Versions{Platform: appversion.Android, Min: "1.2.3", Recommended: "1.10.0",
		StoreURL: "https://play.google.com/store/apps/details?id=app.wf", Version: 3, UpdatedAt: at}
	if got != want {
		t.Fatalf("%+v, нужно %+v", got, want)
	}
	// строка одной платформы не отдаётся другой
	if _, ok, err := r.Get(ctx, appversion.IOS); err != nil || ok {
		t.Fatalf("ios после заведения android: %v %v", ok, err)
	}
}

// Запись версий (Task 15): Put в транзакции — создание (before nil, version 1), правка (before — прежние значения,
// version + 1), те же значения — без записи (version не растёт); List — заведённые платформы по имени.
func TestPutAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "admin")
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	ios := appversion.Versions{Platform: appversion.IOS, Min: "1.0.0", Recommended: "1.2.0", StoreURL: "https://apps.apple.com/app/id1"}
	put := func(v appversion.Versions) (before *appversion.Versions, after appversion.Versions) {
		t.Helper()
		err := db.InTx(ctx, pools.As, func(_ context.Context, tx pgx.Tx) (err error) {
			before, after, err = appversion.Put(ctx, tx, v, at)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return before, after
	}
	before, after := put(ios)
	if before != nil || after.Version != 1 || after.Min != "1.0.0" || after.Recommended != "1.2.0" || !after.UpdatedAt.Equal(at) {
		t.Fatalf("создание: %+v %+v", before, after)
	}
	next := ios
	next.Min = "1.1.0"
	before, after = put(next)
	if before == nil || before.Min != "1.0.0" || before.Version != 1 || after.Version != 2 || after.Min != "1.1.0" {
		t.Fatalf("правка: %+v %+v", before, after)
	}
	before, after = put(next)
	if before == nil || after.Version != 2 || before.Version != 2 {
		t.Fatalf("без изменений: %+v %+v", before, after)
	}
	put(appversion.Versions{Platform: appversion.Android, Min: "2.0.0", Recommended: "2.0.0", StoreURL: "https://play.google.com/store/apps/details?id=a"})
	list, err := appversion.List(ctx, pools.As)
	if err != nil || len(list) != 2 || list[0].Platform != appversion.Android || list[1].Platform != appversion.IOS || list[1].Version != 2 {
		t.Fatalf("список: %+v %v", list, err)
	}
}
