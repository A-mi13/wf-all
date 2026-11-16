package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
)

// Client — откуда пришёл запрос (собирает httpapi.ClientFrom из peer.Info, R19): вид клиента ставит сервер, а не
// тело. Device — тело DeviceInfo мобильного (от BFF — nil); BFFDevice — метка устройства браузера от BFF.
type Client struct {
	Kind      domain.ClientKind
	IP        netip.Addr
	UserAgent string
	Device    *identity.DeviceInput
	BFFDevice string
}

// maxSignupsPerDevice — регистраций с мобильного устройства (правило домена §3.8, R2).
const maxSignupsPerDevice = 2

var (
	errDeviceRequired    = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeDeviceRequired)
	errDeviceSignupLimit = httpx.NewError(http.StatusForbidden, identity.CodeDeviceSignupLimit)
)

// requireDevice — мобильный запрос (не от BFF) без device → 422 identity.device_required (К5).
func (a *App) requireDevice(c Client) error {
	if c.Kind != domain.ClientWeb && c.Device == nil {
		return errDeviceRequired
	}
	return nil
}

// deviceMAC — HMAC устройства ключом keyID: platform ‖ 0x00 ‖ install_id (у мобильного) или метка BFF (веб). Один
// ANDROID_ID на iOS и Android не склеивается — платформа в сообщении.
func (a *App) deviceMAC(keyID string, platform identitydb.DevicePlatform, installID string) ([]byte, error) {
	msg := make([]byte, 0, len(platform)+1+len(installID))
	msg = append(msg, platform...)
	msg = append(msg, 0)
	msg = append(msg, installID...)
	return a.devices.MAC(keyID, domain.LabelDevice, msg)
}

// deviceOf — платформа, идентификатор для HMAC и строки «Где вы вошли»; ok=false — устройства нет (веб без метки).
func deviceOf(c Client) (p identitydb.DevicePlatform, installID string, model, osVer, appVer *string, ok bool) {
	if c.Kind == domain.ClientWeb {
		if c.BFFDevice == "" {
			return "", "", nil, nil, nil, false
		}
		return identitydb.DevicePlatformWeb, c.BFFDevice, nil, nil, nil, true
	}
	if c.Device == nil {
		return "", "", nil, nil, nil, false
	}
	d := c.Device
	if v, ok := domain.NormalizeAppVersion(d.AppVersion); ok {
		appVer = &v
	}
	return identitydb.DevicePlatform(d.Platform), d.InstallID, optStr(d.Model), optStr(d.OSVersion), appVer, true
}

// upsertDevice — строка devices запроса: поиск по HMAC всеми ключами кольца (найденное старым ключом переписывается
// текущим — ленивая ротация §3.8), строка берётся FOR UPDATE до перезаписи (два запроса со старым ключом не дают
// 23505); новой — вставка ON CONFLICT DO NOTHING и перечитывание. Веб без метки BFF — uuid.Nil (R18).
func (a *App) upsertDevice(ctx context.Context, tx pgx.Tx, c Client) (uuid.UUID, error) {
	platform, installID, model, osVer, appVer, ok := deviceOf(c)
	if !ok {
		return uuid.Nil, nil
	}
	ids := a.devices.IDs()
	macs := make([][]byte, len(ids))
	for i, k := range ids {
		m, err := a.deviceMAC(k, platform, installID)
		if err != nil {
			return uuid.Nil, err
		}
		macs[i] = m
	}
	cur, curMAC := ids[0], macs[0]
	q := identitydb.New(tx)
	now := a.clock.Now()
	for range 2 {
		row, err := q.FindDeviceForUpdate(ctx, identitydb.FindDeviceForUpdateParams{Platform: platform, Macs: macs, CurrentKey: cur})
		switch {
		case err == nil:
			if err := q.UpdateDeviceSeen(ctx, identitydb.UpdateDeviceSeenParams{InstallMac: curMAC, KeyID: cur,
				Model: model, OsVersion: osVer, AppVersion: appVer, Now: now, ID: row.ID}); err != nil {
				return uuid.Nil, fmt.Errorf("identity: устройство: %w", err)
			}
			return row.ID, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return uuid.Nil, fmt.Errorf("identity: устройство: %w", err)
		}
		devID, err := q.InsertDevice(ctx, identitydb.InsertDeviceParams{ID: id.New(), Platform: platform,
			InstallMac: curMAC, KeyID: cur, Model: model, OsVersion: osVer, AppVersion: appVer, Now: now})
		if err == nil {
			return devID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("identity: устройство: %w", err)
		}
		// вставку опередила параллельная — перечитать под замком
	}
	return uuid.Nil, errors.New("identity: устройство не найдено после конфликта вставки")
}

// linkDevice — связь устройства с аккаунтом (device_accounts); signedUp — аккаунт создан на этом устройстве.
// Устройства нет (uuid.Nil) — ничего.
func (a *App) linkDevice(ctx context.Context, tx pgx.Tx, deviceID, userID uuid.UUID, signedUp bool) error {
	if deviceID == uuid.Nil {
		return nil
	}
	return identitydb.New(tx).UpsertDeviceAccount(ctx, identitydb.UpsertDeviceAccountParams{DeviceID: deviceID,
		UserID: userID, SignedUp: signedUp, Now: a.clock.Now()})
}

// countSignup — регистрация с устройства (R2): мобильное с signups ≥ 2 → 403 identity.device_signup_limit, иначе
// signups + 1 под FOR UPDATE строки (две параллельные регистрации не проходят обе). Веб и uuid.Nil — без счётчика.
func (a *App) countSignup(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) error {
	if deviceID == uuid.Nil {
		return nil
	}
	q := identitydb.New(tx)
	row, err := q.LockDeviceSignups(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("identity: счётчик регистраций: %w", err)
	}
	a.firstLock("count_signup")
	if row.Platform == identitydb.DevicePlatformWeb {
		return nil
	}
	if row.Signups >= maxSignupsPerDevice {
		return errDeviceSignupLimit
	}
	return q.IncDeviceSignups(ctx, deviceID)
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
