package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"wf/backend/internal/platform/clock"
)

// ErrSessionInvalid — сессии нет, она отозвана или истекла, аккаунт забанен или удалён:
// клиенту — 401 (решает загрузчик identity).
var ErrSessionInvalid = errors.New("auth: сессия недействительна")

// SessionLoader — Principal по id сессии одним запросом (статус, revoked_at, ограничения,
// роли). Реализует identity: таблица sessions — его.
type SessionLoader interface {
	LoadSession(ctx context.Context, sessionID uuid.UUID) (*Principal, error)
}

type SessionLoaderFunc func(ctx context.Context, sessionID uuid.UUID) (*Principal, error)

func (f SessionLoaderFunc) LoadSession(ctx context.Context, id uuid.UUID) (*Principal, error) {
	return f(ctx, id)
}

// NoSessions — до спеки identity: сессий нет, любой токен — 401.
var NoSessions SessionLoader = SessionLoaderFunc(func(context.Context, uuid.UUID) (*Principal, error) {
	return nil, ErrSessionInvalid
})

type entry struct {
	p       *Principal
	expires time.Time
}

type cachedLoader struct {
	next  SessionLoader
	ttl   time.Duration
	clock clock.Clock
	max   int
	group singleflight.Group

	mu      sync.Mutex
	entries map[uuid.UUID]entry
}

// NewCachedLoader — кэш в процессе не дольше ttl (§6.2: 5 с): бан, «выйти везде» и смена
// пароля действуют в пределах ttl. Кэшируется только успех; одновременные промахи одной
// сессии — одно обращение к next. Больше max записей — просроченные выбрасываются, а если
// и это не помогло — кэш очищается целиком (дешевле, чем LRU, и не растёт без предела).
func NewCachedLoader(next SessionLoader, ttl time.Duration, c clock.Clock, max int) SessionLoader {
	return &cachedLoader{next: next, ttl: ttl, clock: c, max: max, entries: make(map[uuid.UUID]entry)}
}

func (l *cachedLoader) LoadSession(ctx context.Context, sid uuid.UUID) (*Principal, error) {
	now := l.clock.Now()
	l.mu.Lock()
	e, ok := l.entries[sid]
	l.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.p, nil
	}
	v, err, _ := l.group.Do(sid.String(), func() (any, error) {
		// срок — от начала загрузки: прочитанное верно на момент запроса к базе, и долгий
		// ответ не продлевает жизнь отозванной сессии сверх ttl
		start := l.clock.Now()
		// загрузка не должна оборваться из-за отмены одного из ждущих запросов
		p, err := l.next.LoadSession(context.WithoutCancel(ctx), sid)
		if err != nil {
			return nil, err
		}
		l.put(sid, p, start.Add(l.ttl))
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Principal), nil
}

func (l *cachedLoader) put(sid uuid.UUID, p *Principal, expires time.Time) {
	now := l.clock.Now()
	if !now.Before(expires) {
		return // загрузка шла дольше ttl — запись родилась просроченной
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) >= l.max {
		for k, e := range l.entries {
			if !now.Before(e.expires) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= l.max {
			clear(l.entries)
		}
	}
	l.entries[sid] = entry{p: p, expires: expires}
}
