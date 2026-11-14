package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"wf/backend/internal/httpapi/public/oapi"
	identityhttp "wf/backend/internal/identity/httpapi"
	"wf/backend/internal/platform/peer"
)

var body = &oapi.DeviceInfo{Platform: "ios", InstallId: "6F9619FF-8B86-4D01-B42D-00CF4FC964FF", Model: "iPhone15,2",
	OsVersion: "18.1", AppVersion: "1.4"}

// R19: вид клиента — только по peer. От BFF — web, метка устройства из peer, тело device игнорируется; иначе —
// mobile с устройством из тела (нет тела — Device nil, requireDevice ответит 422).
func TestClientFromKindByPeer(t *testing.T) {
	t.Parallel()
	ip := netip.MustParseAddr("203.0.113.7")
	bff := peer.With(context.Background(), peer.Info{IP: ip, ViaBFF: true, Device: "bff-label"})
	c := identityhttp.ClientFrom(bff, body)
	if c.Kind != "web" || c.Device != nil || c.BFFDevice != "bff-label" || c.IP != ip {
		t.Fatalf("BFF: %+v", c)
	}
	direct := peer.With(context.Background(), peer.Info{IP: ip})
	c = identityhttp.ClientFrom(direct, body)
	if c.Kind != "mobile" || c.Device == nil || c.Device.InstallID != body.InstallId || c.Device.AppVersion != "1.4" || c.BFFDevice != "" {
		t.Fatalf("мобильный: %+v", c)
	}
	if c = identityhttp.ClientFrom(direct, nil); c.Kind != "mobile" || c.Device != nil {
		t.Fatalf("мобильный без тела: %+v", c)
	}
}

// RequestInfo кладёт User-Agent (httpx.UserAgent: ≤ 512 байт) в ctx операции — ClientFrom его видит.
func TestRequestInfoCarriesUserAgent(t *testing.T) {
	t.Parallel()
	var got string
	h := identityhttp.RequestInfo(func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		got = identityhttp.ClientFrom(ctx, nil).UserAgent
		return nil, nil
	}, "login")
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/login", nil)
	r.Header.Set("User-Agent", strings.Repeat("я", 400))
	if _, err := h(context.Background(), httptest.NewRecorder(), r, nil); err != nil {
		t.Fatal(err)
	}
	if got == "" || len(got) > 512 {
		t.Fatalf("UA %d байт", len(got))
	}
}
