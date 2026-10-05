package appversion_test

import (
	"context"
	"testing"
	"time"

	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Чтение под ролью api (права прода из grants.sql): нет строки — ok=false, не ошибка.
func TestReaderGet(t *testing.T) {
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
