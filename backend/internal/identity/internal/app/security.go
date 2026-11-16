package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/id"
)

// SecurityEvent — запись журнала безопасности аккаунта (спека §8.3), она же ссылка писем. Meta — без ПД (виды,
// счётчики, id); Address — почта для письма после смены почты и удаления: только в meta_enc (Seal кольцом секретов,
// aad = id записи), стирается чисткой через 7 дней.
type SecurityEvent struct {
	UserID    uuid.UUID // uuid.Nil — без аккаунта
	Kind      domain.SecurityKind
	SessionID uuid.UUID
	DeviceID  uuid.UUID
	IP        netip.Addr
	IPCity    string
	UserAgent string
	Meta      map[string]any
	Address   string
}

// errNoSecrets — запись с адресом у сборки без кольца секретов (NewMaintenance admin-api, N3): 500, не паника.
var errNoSecrets = errors.New("identity: адрес в журнале без кольца секретов")

// securityEvent — запись журнала в tx действия; id — ref писем.
func (a *App) securityEvent(ctx context.Context, tx pgx.Tx, e SecurityEvent) (uuid.UUID, error) {
	rowID := id.New()
	meta := e.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: meta журнала: %w", err)
	}
	p := identitydb.InsertSecurityEventParams{ID: rowID, Kind: string(e.Kind), Meta: metaJSON, Now: a.clock.Now(),
		UserID: optUUID(e.UserID), SessionID: optUUID(e.SessionID), DeviceID: optUUID(e.DeviceID),
		IpCity: optStr(e.IPCity), UserAgent: optStr(e.UserAgent)}
	if e.IP.IsValid() {
		ip := e.IP
		p.Ip = &ip
	}
	if e.Address != "" {
		if a.secrets == nil {
			return uuid.Nil, errNoSecrets
		}
		ct, keyID, err := a.secrets.Seal([]byte(e.Address), rowID[:])
		if err != nil {
			return uuid.Nil, err
		}
		p.MetaEnc, p.KeyID = ct, &keyID
	}
	if err := identitydb.New(tx).InsertSecurityEvent(ctx, p); err != nil {
		return uuid.Nil, fmt.Errorf("identity: журнал безопасности: %w", err)
	}
	return rowID, nil
}

func optUUID(v uuid.UUID) *uuid.UUID {
	if v == uuid.Nil {
		return nil
	}
	return &v
}
