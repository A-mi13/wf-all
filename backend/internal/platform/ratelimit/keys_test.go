package ratelimit_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/ratelimit"
)

func TestKeys(t *testing.T) {
	id := uuid.MustParse("0192f1a0-0000-7000-8000-000000000001")
	for _, c := range []struct{ got, want string }{
		{ratelimit.IPKey("staff_auth", netip.MustParseAddr("203.0.113.7")), "ip:staff_auth:203.0.113.7"},
		{ratelimit.IPKey("default", netip.MustParseAddr("2001:db8::1")), "ip:default:2001:db8::1"},
		{ratelimit.SubjectKey("default", auth.Subject{Kind: auth.SubjectUser, ID: id}), "user:default:" + id.String()},
		{ratelimit.SubjectKey("default", auth.Subject{Kind: auth.SubjectStaff, ID: id}), "staff:default:" + id.String()},
		{ratelimit.SessionDeviceKey("default", id), "device:default:s:" + id.String()},
		{ratelimit.BFFDeviceKey("auth", "dev-a"), "device:auth:b:dev-a"},
		{ratelimit.EmailKey("staff_login", []byte{0xfb, 0xff, 0x00, 0x10}), "email:staff_login:-_8AEA"},
	} {
		if c.got != c.want {
			t.Errorf("ключ %q, нужно %q", c.got, c.want)
		}
	}
	// значение почты — без ':' и без самой почты: HMAC любой длины кодируется base64url
	mac := []byte(strings.Repeat("\xff", 32))
	if k := ratelimit.EmailKey("staff_login", mac); strings.Count(k, ":") != 2 || strings.ContainsAny(k, "+/=") {
		t.Fatalf("EmailKey: %q", k)
	}
}
