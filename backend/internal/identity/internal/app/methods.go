package app

import (
	"context"
)

// Способы входа (спека §4.4, R4) — Task 12.

// Ключи флагов способов входа (спека §4.1; правила — platform/flags/rules.go).
const (
	flagApple        = "auth_apple"
	flagAppleLogin   = "auth_apple_login"
	flagGoogle       = "auth_google"
	flagGoogleLogin  = "auth_google_login"
	flagPasskeys     = "auth_passkeys" //nolint:gosec // G101: ключ флага функций, не учётные данные
	flagPasskeysAuth = flagPasskeysLogin
)

// AuthMethodsView — способы входа (getAuthMethods).
type AuthMethodsView struct {
	Password                                           bool
	AppleSignup, AppleLogin, GoogleSignup, GoogleLogin bool
	PasskeysRegister, PasskeysLogin                    bool
}

// AuthMethods — способ включён, если включён его флаг «везде» (EnabledGlobally учитывает Requires: auth_google при
// выключенном auth_apple — выключен, R12) и провайдер настроен у api (Providers, R4). Пароль — всегда.
func (a *App) AuthMethods(ctx context.Context) (AuthMethodsView, error) {
	v := AuthMethodsView{Password: true}
	for _, s := range []struct {
		flag  string
		ready bool
		dst   *bool
	}{
		{flagApple, a.providers.Apple(), &v.AppleSignup},
		{flagAppleLogin, a.providers.Apple(), &v.AppleLogin},
		{flagGoogle, a.providers.Google(), &v.GoogleSignup},
		{flagGoogleLogin, a.providers.Google(), &v.GoogleLogin},
		{flagPasskeys, a.providers.Passkeys(), &v.PasskeysRegister},
		{flagPasskeysAuth, a.providers.Passkeys(), &v.PasskeysLogin},
	} {
		if !s.ready {
			continue
		}
		on, err := a.flags.EnabledGlobally(ctx, s.flag)
		if err != nil {
			return AuthMethodsView{}, err
		}
		*s.dst = on
	}
	return v, nil
}
