// Package httpapi — публичные ручки модуля identity (тег identity контракта public, спека identity §6): вход и
// регистрация, аккаунт, сессии, 2FA, ключи доступа, соцвходы, капитанство. Handler встраивается в
// internal/httpapi/public.Server: тег операции = модуль-реализатор (apitest.TagViolations).
//
// Раскладка (план I-2, Task 8, R5): операции — по файлам-владельцам областей (signup, login, sessions, password,
// account, deletion, mfa, mfa_reset, pending_i3, pending_i4 — Приложение 1 плана); здесь только Handler, New,
// разбор клиента запроса. Нулевой Handler — каждая операция 501 http.not_implemented; нереализованная операция —
// 501 и с App, и стоит в pendingOps стража TestNoStubOperations (internal/httpapi/public).
package httpapi

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
)

// Handler — ручки identity. Нулевое значение — заглушка: каждая операция отвечает 501.
type Handler struct {
	app *app.App
}

// New — ручки на сценариях identity (app.New): забытая зависимость — ошибка сборки, а не паника в запросе.
func New(d identity.Deps) (Handler, error) {
	a, err := app.New(d)
	if err != nil {
		return Handler{}, err
	}
	return Handler{app: a}, nil
}

// errNotImplemented — ответ операции, которую этап спеки ещё не реализовал.
var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

type requestKey struct{}

// RequestInfo — strict-middleware публичного API: кладёт в ctx то, что strict-сервер не передаёт операции
// (User-Agent запроса, httpx.UserAgent: ≤ 512 байт, валидный UTF-8). Ставит public.NewHandler.
func RequestInfo(f oapi.StrictHandlerFunc, _ string) oapi.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return f(context.WithValue(ctx, requestKey{}, httpx.UserAgent(r)), w, r, req)
	}
}

// ClientFrom — клиент запроса для сценариев (R19): вид — только по peer (ViaBFF → web, иначе mobile); у BFF тело
// device игнорируется, устройство — метка peer.Info.Device; IP — peer.Info.IP.
func ClientFrom(ctx context.Context, body *oapi.DeviceInfo) app.Client {
	p := peer.From(ctx)
	ua, _ := ctx.Value(requestKey{}).(string)
	c := app.Client{IP: p.IP, UserAgent: ua}
	if p.ViaBFF {
		c.Kind = domain.ClientWeb
		c.BFFDevice = p.Device
		return c
	}
	c.Kind = domain.ClientMobile
	if body != nil {
		d := &identity.DeviceInput{Platform: string(body.Platform), InstallID: body.InstallId, Model: body.Model,
			OSVersion: body.OsVersion, AppVersion: body.AppVersion}
		if body.DeviceCheckToken != nil {
			d.DeviceCheckToken = *body.DeviceCheckToken
		}
		if in := body.Integrity; in != nil {
			d.Integrity = &identity.DeviceIntegrity{ChallengeID: in.ChallengeId, Kind: string(in.Kind),
				Payload: in.Payload}
			if in.KeyId != nil {
				d.Integrity.KeyID = *in.KeyId
			}
		}
		c.Device = d
	}
	return c
}
