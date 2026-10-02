package auth

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
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
	next       SessionLoader
	ttl        time.Duration
	clock      clock.Clock
	maxEntries int
	group      singleflight.Group
	waiters    atomic.Int32 // вызовы, ждущие общую загрузку (видят тесты)

	mu      sync.Mutex
	entries map[uuid.UUID]entry
}

// NewCachedLoader — кэш в процессе не дольше ttl (§6.2: 5 с): бан, «выйти везде» и смена
// пароля действуют в пределах ttl. Кэшируется только успех; одновременные промахи одной
// сессии — одно обращение к next, и у этой загрузки свой срок ttl: зависшая база даёт
// ошибку (500), а не держит запросы и не раздаёт состояние старше ttl. Вызывающий, чей ctx
// истёк, уходит сразу, общая загрузка продолжается для остальных. Больше maxEntries
// записей — просроченные выбрасываются, а если и это не помогло — кэш очищается целиком
// (дешевле, чем LRU, и не растёт без предела). ttl <= 0 или maxEntries <= 0 — паника:
// ошибка конфигурации на старте (срок загрузки 0 положил бы всю аутентификацию).
func NewCachedLoader(next SessionLoader, ttl time.Duration, c clock.Clock, maxEntries int) SessionLoader {
	if ttl <= 0 || maxEntries <= 0 {
		panic(fmt.Sprintf("auth: NewCachedLoader: ttl %v и maxEntries %d должны быть > 0", ttl, maxEntries))
	}
	return &cachedLoader{next: next, ttl: ttl, clock: c, maxEntries: maxEntries, entries: make(map[uuid.UUID]entry)}
}

func (l *cachedLoader) LoadSession(ctx context.Context, sid uuid.UUID) (*Principal, error) {
	now := l.clock.Now()
	l.mu.Lock()
	e, ok := l.entries[sid]
	l.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.p, nil
	}
	ch := l.group.DoChan(sid.String(), func() (v any, err error) {
		// загрузка идёт в горутине singleflight: recoverer сервера её панику не видит, а
		// DoChan переподнимает панику там же — без перехвата падает весь процесс
		defer func() {
			if r := recover(); r != nil {
				v, err = nil, fmt.Errorf("auth: паника в загрузке сессии: %v\n%s", r, debug.Stack())
			}
		}()
		// срок — от начала загрузки: прочитанное верно на момент запроса к базе, и долгий
		// ответ не продлевает жизнь отозванной сессии сверх ttl
		start := l.clock.Now()
		// отмена одного из ждущих не обрывает общую загрузку, но у неё свой срок — ttl:
		// WithoutCancel снимает и срок вызывающего
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.ttl)
		defer cancel()
		p, err := l.next.LoadSession(lctx, sid)
		if err != nil {
			return nil, err
		}
		l.put(sid, p, start.Add(l.ttl))
		return p, nil
	})
	// DoChan уже записал вызов в общую загрузку
	l.waiters.Add(1)
	defer l.waiters.Add(-1)
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*Principal), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *cachedLoader) put(sid uuid.UUID, p *Principal, expires time.Time) {
	now := l.clock.Now()
	if !now.Before(expires) {
		return // загрузка шла дольше ttl — запись родилась просроченной
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) >= l.maxEntries {
		for k, e := range l.entries {
			if !now.Before(e.expires) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= l.maxEntries {
			clear(l.entries)
		}
	}
	l.entries[sid] = entry{p: p, expires: expires}
}
