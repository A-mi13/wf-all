package app_test

import (
	"context"
	"testing"

	"wf/backend/internal/platform/keys"
	"wf/backend/internal/staff/internal/app"
)

// Уборка №5 (minor Task 7): Rekey параллельно с MFASetup той же учётки не возвращает старый секрет.
// Rekey прочитал секрет на старом ключе, а MFASetup тем временем выпустил новый: без замков строки
// в Rekey перешифровка записала бы старый секрет поверх нового — приложение сотрудника (QR нового
// секрета) перестало бы совпадать с базой. Барьер: чужая транзакция держит строку staff_mfa, MFASetup
// встаёт за ней первым, Rekey — вторым; после освобождения в базе — секрет, который выдал MFASetup.
func TestRekeyConcurrentMFASetup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	oldRing, oldKey := newRing(t)
	e.deps.Keys, e.ring = oldRing, oldRing
	e.app = app.New(e.deps)
	_, s := e.setupSession("ivan.p@wf.example")
	if _, _, err := e.app.MFASetup(ctx, s); err != nil { // секрет на старом ключе
		t.Fatal(err)
	}
	rotated, newKey := newRing(t, oldKey)
	ra := e.withRing(rotated)

	hold, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM staff_mfa WHERE staff_id = $1 FOR UPDATE`, s.StaffID); err != nil {
		t.Fatal(err)
	}

	type setupRes struct {
		b32 string
		err error
	}
	setupDone := make(chan setupRes, 1)
	go func() {
		b32, _, err := ra.MFASetup(ctx, s)
		setupDone <- setupRes{b32, err}
	}()
	e.waitLockWaiters(1) // MFASetup держит учётку и ждёт строку секрета
	rekeyDone := make(chan error, 1)
	var rep app.RekeyReport
	go func() {
		var err error
		rep, err = ra.Rekey(ctx)
		rekeyDone <- err
	}()
	e.waitLockWaiters(2) // Rekey встал за замком
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	sr := <-setupDone
	if sr.err != nil {
		t.Fatalf("MFASetup: %v", sr.err)
	}
	if err := <-rekeyDone; err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if rep.MFA != 0 || rep.MFAOnOldKeys != 0 {
		t.Fatalf("Rekey перешифровал уже новый секрет: %+v", rep)
	}
	// в базе — секрет, выданный MFASetup, на текущем ключе: подтверждение его кодом проходит без
	// старого ключа в кольце
	if n := e.count(`SELECT count(*) FROM staff_mfa WHERE staff_id = $1 AND key_id = $2`, s.StaffID, rotated.CurrentID()); n != 1 {
		t.Fatal("секрет не на текущем ключе")
	}
	onlyNew, err := keys.NewRing([]string{newKey})
	if err != nil {
		t.Fatal(err)
	}
	m := member{ID: s.StaffID, Secret: decodeSecret(t, sr.b32)}
	if _, err := e.withRing(onlyNew).MFAConfirm(ctx, s, e.totp(m), metaWeb); err != nil {
		t.Fatalf("код секрета из MFASetup не подошёл — Rekey вернул старый секрет: %v", err)
	}
}
