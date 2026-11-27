package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/cmd/internal/mediapurposes"
	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/token"
)

// mediaAt — «сейчас» тестов media.
var mediaAt = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// fsBaseURL — внешний адрес api тестов fs (на нём /_blob и /_media).
const fsBaseURL = "http://127.0.0.1:8080"

// mediaAPI — публичный api на сборке media процесса (newMedia, devMedia, mediaE2E — как в run) под ролью api, часы —
// подделка; сессия игрока — токен настоящего Issuer и загрузчик-подделка.
type mediaAPI struct {
	h     http.Handler
	kit   mediaKit
	clk   *clocktest.Fake
	pools dbtest.Pools
	iss   *token.Issuer
	users sync.Map // session_id → user_id
}

// openTestBlob — хранилище режима mode через ту же фабрику, что у run (fs — во временном каталоге).
func openTestBlob(t *testing.T, mode string, clk *clocktest.Fake) blob.Opened {
	t.Helper()
	c := config.Blob{Backend: mode}
	if mode == "fs" {
		c.FSRoot, c.FSBaseURL, c.DevSignKey = t.TempDir(), fsBaseURL, testSeed
	}
	o, err := blobopen.Open(context.Background(), c, clk)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func newMediaAPI(t *testing.T, mode string) *mediaAPI {
	t.Helper()
	clk := clocktest.New(mediaAt)
	pools := dbtest.NewPoolsAs(t, "api")
	reg, err := mediapurposes.Registry()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	limiter := ratelimit.NewPG(pools.As, clk, ratelimit.Players)
	kit, err := newMedia(log, pools.As, openTestBlob(t, mode, clk), reg, clk, limiter)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := token.ParseKeys([]string{testSeed})
	if err != nil {
		t.Fatal(err)
	}
	m := &mediaAPI{kit: kit, clk: clk, pools: pools, iss: token.NewIssuer(keys, clk)}
	h, err := public.NewHandler(log, public.Options{
		DB:     pools.As,
		Tokens: m.iss,
		Sessions: auth.SessionLoaderFunc(func(_ context.Context, sid uuid.UUID) (*auth.Principal, error) {
			uid, ok := m.users.Load(sid)
			if !ok {
				return nil, auth.ErrSessionInvalid
			}
			return &auth.Principal{UserID: uid.(uuid.UUID), SessionID: sid, Status: "active"}, nil
		}),
		Limiter:     limiter,
		RateRules:   ratelimit.DefaultRules(),
		Geo:         geo.New(pools.As),
		AppVersions: appversion.NewReader(pools.As),
		Media:       kit.devMedia(log),
		E2E:         mediaE2E(kit, pools.As, log),
	})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	return m
}
