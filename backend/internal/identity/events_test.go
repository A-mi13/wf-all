package identity_test

import (
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/events"
)

// -update перезаписывает golden-файлы (go test ./internal/identity/ -run TestEventPayloadsGolden -update), затем
// node_modules/.bin/prettier --write backend/internal/identity/testdata/events — иначе pre-commit отклонит формат.
var update = flag.Bool("update", false, "перезаписать testdata/events/*.golden.json")

var (
	userID  = uuid.MustParse("01930000-0000-7000-8000-000000000001")
	cityID  = uuid.MustParse("01930000-0000-7000-8000-000000000002")
	resetID = uuid.MustParse("01930000-0000-7000-8000-000000000003")
	sess1   = uuid.MustParse("01930000-0000-7000-8000-000000000011")
	sess2   = uuid.MustParse("01930000-0000-7000-8000-000000000012")
	at      = time.Date(2026, 12, 14, 9, 30, 0, 0, time.UTC)
	user    = identity.UserRef{ID: userID, Version: 7}
)

// allEvents — по конструктору на событие каталога I-2 (спека §8.1); имя — golden-файл.
func allEvents() map[string]events.Event {
	reset := identity.MfaResetRef{ID: resetID, Version: 1}
	return map[string]events.Event{
		"user_registered":         identity.UserRegistered(user, cityID, "password", "ios"),
		"user_deletion_scheduled": identity.UserDeletionScheduled(user, at),
		"user_deletion_cancelled": identity.UserDeletionCancelled(user),
		"user_deleted":            identity.UserDeleted(user),
		"user_email_changed":      identity.UserEmailChanged(user),
		"nickname_changed":        identity.NicknameChanged(user),
		"password_changed":        identity.PasswordChanged(user),
		"mfa_changed":             identity.MfaChanged(user, true),
		"sessions_revoked":        identity.SessionsRevoked(user, []uuid.UUID{sess1, sess2}, "password_changed"),
		"restrictions_changed":    identity.RestrictionsChanged(user, "active", []string{"match_join"}),
		"mfa_reset_scheduled":     identity.MfaResetScheduled(reset, userID, at),
		"mfa_reset_cancelled":     identity.MfaResetCancelled(identity.MfaResetRef{ID: resetID, Version: 2}, userID, at),
	}
}

// Конверт и payload каждого события равны golden-файлу: подписчики будущих модулей читают ровно эту схему.
func TestEventPayloadsGolden(t *testing.T) {
	evs := allEvents()
	if len(evs) != len(identity.EventTypes()) {
		t.Fatalf("конструкторов %d, событий в каталоге %d", len(evs), len(identity.EventTypes()))
	}
	types := map[string]bool{}
	for name, e := range evs {
		types[e.Type] = true
		if e.Type != "identity."+name || e.SchemaVersion != 1 || e.AggregateID == uuid.Nil || e.AggregateVersion < 1 {
			t.Errorf("%s: конверт %+v", name, e)
		}
		got, err := json.MarshalIndent(struct {
			Type             string `json:"type"`
			SchemaVersion    int    `json:"schema_version"`
			AggregateType    string `json:"aggregate_type"`
			AggregateID      string `json:"aggregate_id"`
			AggregateVersion int64  `json:"aggregate_version"`
			Payload          any    `json:"payload"`
		}{e.Type, e.SchemaVersion, e.AggregateType, e.AggregateID.String(), e.AggregateVersion, e.Payload}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "events", name+".golden.json")
		if *update {
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := fs.ReadFile(os.DirFS(filepath.Join("testdata", "events")), name+".golden.json")
		if err != nil {
			t.Fatalf("%s: %v (сгенерировать — -update)", name, err)
		}
		// сравнение по значению JSON: golden-файлы форматирует prettier (pre-commit), байты у него свои
		var g, w any
		if err := json.Unmarshal(got, &g); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want, &w); err != nil {
			t.Fatalf("%s: golden не JSON: %v", name, err)
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s: payload расходится с golden\nесть:\n%s\nждали:\n%s", name, got, want)
		}
	}
	for _, typ := range identity.EventTypes() {
		if !types[typ] {
			t.Errorf("событие %s без конструктора в тесте", typ)
		}
	}
}

// Payload — только идентификаторы (спека §8.1): ни почты, ни телефона, ни даты рождения, ни ника, ни IP — по ключам
// JSON любой вложенности.
func TestEventPayloadsNoPII(t *testing.T) {
	banned := []string{"email", "phone", "birth", "nickname", "ip", "install", "token", "password"}
	for name, e := range allEvents() {
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		for _, k := range jsonKeys(v) {
			for _, b := range banned {
				if k == b || strings.HasPrefix(k, b+"_") || strings.HasSuffix(k, "_"+b) {
					t.Errorf("%s: ключ %q — персональные данные или секрет в payload", name, k)
				}
			}
		}
		if reflect.TypeOf(e.Payload).Kind() != reflect.Struct {
			t.Errorf("%s: payload — не структура", name)
		}
	}
}

func jsonKeys(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, sub := range x {
			out = append(out, k)
			out = append(out, jsonKeys(sub)...)
		}
	case []any:
		for _, sub := range x {
			out = append(out, jsonKeys(sub)...)
		}
	}
	return out
}

// Сверх 100 идентификаторов сессий в payload нет (спека §8.1): лишние отбрасываются, порядок сохраняется.
func TestSessionsRevokedCap(t *testing.T) {
	ids := make([]uuid.UUID, 150)
	for i := range ids {
		ids[i] = uuid.New()
	}
	p := identity.SessionsRevoked(user, ids, "banned").Payload.(identity.SessionsRevokedPayload)
	if len(p.SessionIDs) != 100 || p.SessionIDs[0] != ids[0] || p.SessionIDs[99] != ids[99] {
		t.Fatalf("%d", len(p.SessionIDs))
	}
	if p := identity.SessionsRevoked(user, nil, "banned").Payload.(identity.SessionsRevokedPayload); p.SessionIDs == nil {
		t.Fatal("пустой список — null вместо []")
	}
}
