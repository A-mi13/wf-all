// Package risk — когда требовать проверку «человек ли» (спека бэкенда §6.7): мягкие пороги на
// сигналы — неудачные входы на аккаунт или IP, всплеск регистраций из подсети, новое
// устройство, аномальная частота действий. Превышение — повод для PoW, а не бан: аномалии —
// в очередь модерации, не автобан.
package risk

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"wf/backend/internal/platform/ratelimit"
)

type Signal string

const (
	LoginFailed Signal = "login_failed" // неудачный вход — ключи: аккаунт, IP
	Signup      Signal = "signup"       // регистрация — ключ: подсеть (Subnet)
	NewDevice   Signal = "new_device"   // вход с нового устройства — ключ: аккаунт
	Activity    Signal = "activity"     // частота действий — ключ: пользователь
)

// DefaultPolicies — стартовые пороги; меняются в спеке identity по опыту.
func DefaultPolicies() map[Signal]ratelimit.Policy {
	return map[Signal]ratelimit.Policy{
		LoginFailed: {Limit: 5, Period: 15 * time.Minute},
		Signup:      {Limit: 20, Period: time.Hour},
		NewDevice:   {Limit: 3, Period: 24 * time.Hour},
		Activity:    {Limit: 120, Period: time.Minute},
	}
}

var ErrUnknownSignal = errors.New("risk: у сигнала нет политики")

type Assessor struct {
	l        ratelimit.Limiter
	policies map[Signal]ratelimit.Policy
}

func NewAssessor(l ratelimit.Limiter, policies map[Signal]ratelimit.Policy) (*Assessor, error) {
	for s, p := range policies {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("risk: %s: %w", s, err)
		}
	}
	return &Assessor{l: l, policies: policies}, nil
}

func (a *Assessor) policy(s Signal) (ratelimit.Policy, error) {
	p, ok := a.policies[s]
	if !ok || p.Limit == 0 {
		return ratelimit.Policy{}, fmt.Errorf("%w: %s", ErrUnknownSignal, s)
	}
	return p, nil
}

// Record — учесть событие сигнала (неудачный вход, регистрацию).
func (a *Assessor) Record(ctx context.Context, s Signal, key string) error {
	p, err := a.policy(s)
	if err != nil {
		return err
	}
	return a.l.Add(ctx, "risk:"+string(s)+":"+key, p)
}

// Suspicious — порог сигнала превышен хотя бы по одному ключу.
func (a *Assessor) Suspicious(ctx context.Context, s Signal, keys ...string) (bool, error) {
	p, err := a.policy(s)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		over, err := a.l.Over(ctx, "risk:"+string(s)+":"+k, p)
		if err != nil || over {
			return over, err
		}
	}
	return false, nil
}

// Subnet — ключ подсети для сигналов: /24 у IPv4, /64 у IPv6. Только сигнал, не 429 (§6.6).
func Subnet(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	ip = ip.Unmap()
	bits := 64
	if ip.Is4() {
		bits = 24
	}
	p, err := ip.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}
