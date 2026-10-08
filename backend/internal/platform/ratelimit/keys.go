package ratelimit

import (
	"encoding/base64"
	"net/netip"

	"wf/backend/internal/platform/auth"
)

// Ключ лимита — <вид>:<класс>:<значение>. Виды не пересекаются: ip, user и staff (субъект запроса),
// device (s: — устройство сессии, b: — метка BFF) считает Middleware; email — сценарии, которые
// считают сами через Limiter (вход сотрудника по почте, решение D7). Ключ собирается только этими
// функциями: так счётчик отказов сессии в конвейере admin-api (Limiter.Add) попадает в тот же
// IP-лимит класса, что и запросы через Middleware.

// IPKey — ключ адреса клиента (peer) в классе.
func IPKey(class string, ip netip.Addr) string {
	return "ip:" + class + ":" + ip.String()
}

// SubjectKey — ключ субъекта: user:<класс>:<id> у игрока, staff:<класс>:<id> у сотрудника.
// Игрок и сотрудник считаются раздельно, даже если id совпали бы.
func SubjectKey(class string, s auth.Subject) string {
	return string(s.Kind) + ":" + class + ":" + s.ID.String()
}

// EmailKey — ключ нормализованной почты: mac — HMAC почты (keys.Ring.MAC), сама почта в
// rate_limits не хранится. base64url без заполнения — без ':' в значении.
func EmailKey(class string, mac []byte) string {
	return "email:" + class + ":" + base64.RawURLEncoding.EncodeToString(mac)
}
