package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
)

// problemCode — код ошибки клиента ("" — не ProblemError).
func problemCode(err error) string {
	var pe httpx.ProblemError
	if errors.As(err, &pe) {
		return pe.Problem().Code
	}
	return ""
}

func upsert(t *testing.T, e *env, c Client) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = e.a.upsertDevice(ctx, tx, c)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// Ленивая ротация (§3.8): устройство создано под K1, кольцо [K2, K1] — найдено то же, key_id = K2, install_mac
// пересчитан текущим ключом.
func TestUpsertDeviceFindsByOldKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := mobile("ios", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF")
	first := upsert(t, e, c)
	k2 := genKey(t)
	e.a.devices = newRing(t, k2, e.devKeys[0])
	again := upsert(t, e, c)
	if again != first {
		t.Fatalf("новое устройство %s вместо %s", again, first)
	}
	var keyID string
	var mac []byte
	var appVer *string
	if err := e.owner.QueryRow(context.Background(), `SELECT key_id, install_mac, app_version FROM devices WHERE id = $1`,
		first).Scan(&keyID, &mac, &appVer); err != nil {
		t.Fatal(err)
	}
	want, _ := e.a.deviceMAC(e.a.devices.CurrentID(), "ios", c.Device.InstallID)
	if keyID != e.a.devices.CurrentID() || string(mac) != string(want) {
		t.Fatalf("key_id %s, ждали %s; install_mac пересчитан: %v", keyID, e.a.devices.CurrentID(), string(mac) == string(want))
	}
	if appVer == nil || *appVer != "1.4.0" {
		t.Fatalf("app_version = %v, ждали 1.4.0 (нормализация)", appVer)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM devices`); n != 1 {
		t.Fatalf("устройств %d", n)
	}
}

// Один install_id на разных платформах — разные устройства (платформа в сообщении HMAC).
func TestUpsertDevicePlatformInMAC(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if upsert(t, e, mobile("ios", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF")) ==
		upsert(t, e, mobile("android", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF")) {
		t.Fatal("iOS и Android с одним install_id склеились")
	}
}

// Веб (R18, R19): метка BFF → platform = web; без метки — uuid.Nil; device в теле от BFF игнорируется.
func TestUpsertDeviceWeb(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := web("bff-label-1")
	c.Device = &identity.DeviceInput{Platform: "ios", InstallID: "6F9619FF-8B86-4D01-B42D-00CF4FC964FF"}
	id := upsert(t, e, c)
	var platform string
	var model *string
	if err := e.owner.QueryRow(context.Background(), `SELECT platform, model FROM devices WHERE id = $1`, id).
		Scan(&platform, &model); err != nil {
		t.Fatal(err)
	}
	if platform != "web" || model != nil {
		t.Fatalf("platform %s, model %v — тело device от BFF попало в строку", platform, model)
	}
	if got := upsert(t, e, web("")); got != uuid.Nil {
		t.Fatalf("веб без метки: %s, ждали uuid.Nil", got)
	}
}

// К5: мобильный без device — 422 identity.device_required; BFF без device — можно.
func TestRequireDevice(t *testing.T) {
	t.Parallel()
	a := &App{}
	if got := problemCode(a.requireDevice(Client{Kind: domain.ClientMobile})); got != identity.CodeDeviceRequired {
		t.Fatalf("мобильный без device: %q", got)
	}
	if err := a.requireDevice(mobile("ios", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF")); err != nil {
		t.Fatal(err)
	}
	if err := a.requireDevice(web("")); err != nil {
		t.Fatalf("BFF без device: %v", err)
	}
}

// R2: два параллельных countSignup на устройстве с signups = 1 — ровно один успех, второй 403
// identity.device_signup_limit; веб — без счётчика. Барьер afterFirstLock: первый ждёт второго до 200 мс (под FOR
// UPDATE второй стоит на замке — барьер истекает; без замка оба прочли 1 и прошли бы оба).
func TestCountSignupLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for round := range 5 {
		dev := upsert(t, e, mobile("android", "android-id-000000"+string(rune('a'+round))))
		if _, err := e.owner.Exec(context.Background(), `UPDATE devices SET signups = 1 WHERE id = $1`, dev); err != nil {
			t.Fatal(err)
		}
		wait := pairBarrier(200 * time.Millisecond)
		e.a.hooks = &hooks{afterFirstLock: func(op string) {
			if op == "count_signup" {
				wait()
			}
		}}
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Go(func() {
				<-start
				errs[i] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error { return e.a.countSignup(ctx, tx, dev) })
			})
		}
		close(start)
		wg.Wait()
		e.a.hooks = nil
		ok, limited := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case problemCode(err) == identity.CodeDeviceSignupLimit:
				limited++
			default:
				t.Fatalf("раунд %d: %v", round, err)
			}
		}
		if ok != 1 || limited != 1 {
			t.Fatalf("раунд %d: успехов %d, отказов %d", round, ok, limited)
		}
		if n := count(t, e.owner, `SELECT signups FROM devices WHERE id = $1`, dev); n != 2 {
			t.Fatalf("раунд %d: signups = %d", round, n)
		}
	}
	webDev := upsert(t, e, web("bff-label-2"))
	for range 3 {
		if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error { return e.a.countSignup(ctx, tx, webDev) }); err != nil {
			t.Fatalf("веб: %v", err)
		}
	}
}

// Два параллельных upsertDevice по старому ключу — одна строка, ни одного 23505 (FOR UPDATE до перезаписи).
func TestUpsertDeviceOldKeyConcurrent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for round := range 5 {
		c := mobile("ios", "concurrent-install-"+string(rune('a'+round)))
		e.a.devices = newRing(t, e.devKeys...)
		first := upsert(t, e, c)
		e.a.devices = newRing(t, append([]string{genKey(t)}, e.devKeys...)...)
		start := make(chan struct{})
		ids := make([]uuid.UUID, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Go(func() {
				<-start
				errs[i] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					ids[i], err = e.a.upsertDevice(ctx, tx, c)
					return err
				})
			})
		}
		close(start)
		wg.Wait()
		for i := range 2 {
			if errs[i] != nil || ids[i] != first {
				t.Fatalf("раунд %d: %v, id %s (ждали %s)", round, errs[i], ids[i], first)
			}
		}
	}
}

// Новое устройство двумя запросами разом — одна строка (ON CONFLICT DO NOTHING и перечитывание под замком).
func TestUpsertDeviceNewConcurrent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := mobile("android", "fresh-install-000001")
	start := make(chan struct{})
	ids := make([]uuid.UUID, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			errs[i] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				ids[i], err = e.a.upsertDevice(ctx, tx, c)
				return err
			})
		})
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || ids[0] != ids[1] {
		t.Fatalf("%v %v, %s %s", errs[0], errs[1], ids[0], ids[1])
	}
}
