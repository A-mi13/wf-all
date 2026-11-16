package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	identityrekey "wf/backend/internal/identity/rekey"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/queue"
)

// Разовые команды api (план I-2, Task 19, R28): ротация кольца identity идёт под ролью api — у воркера нет UPDATE
// и чтения секретов user_mfa и user_identities (спека identity §5.5, I4).

const usage = "без аргументов — сервер; identity rekey [--dry-run]"

// errUsage — неверная разовая команда (неизвестная, лишние аргументы, неизвестный флаг).
var errUsage = errors.New("неверная команда")

func usageError(msg string) error { return fmt.Errorf("%w: %s: %s", errUsage, msg, usage) }

// dispatch — без аргументов — HTTP-сервер (run); с аргументами — разовая команда.
func dispatch(ctx context.Context, args, environ []string, out, logOut io.Writer) error {
	switch {
	case len(args) == 0:
		return run(ctx, environ, logOut)
	case len(args) >= 2 && args[0] == "identity" && args[1] == "rekey":
		return rekey(ctx, args[2:], environ, out, logOut)
	default:
		return usageError(fmt.Sprintf("неизвестная команда %q", strings.Join(args, " ")))
	}
}

// rekeyConfig — то, что нужно команде из окружения api: журнал, база роли api и кольцо identity (остальное
// окружение api команде не нужно и не проверяется).
type rekeyConfig struct {
	Log      config.Log
	DB       config.DB
	Identity config.Identity `envPrefix:"IDENTITY_"`
}

// rekey — api identity rekey [--dry-run]: перешифровка долгоживущих секретов identity текущим ключом
// API_IDENTITY_SECRET_KEYS (порядок ротации — docs/deploy-dev.md). Синхронно, задач не ставит.
func rekey(ctx context.Context, args, environ []string, out, logOut io.Writer) error {
	fs := flag.NewFlagSet("identity rekey", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "только отчёт, без записи")
	if err := fs.Parse(args); err != nil {
		return usageError(fmt.Sprintf("identity rekey: %v", err))
	}
	if fs.NArg() > 0 {
		return usageError(fmt.Sprintf("identity rekey: лишние аргументы %q", strings.Join(fs.Args(), " ")))
	}
	cfg, err := config.Load[rekeyConfig]("API_", environ)
	if err != nil {
		return err
	}
	// кольцо — до базы: битое значение — отказ без подключения
	secrets, err := keys.NewRing(cfg.Identity.SecretKeys)
	if err != nil {
		return fmt.Errorf("API_IDENTITY_SECRET_KEYS: %w", err)
	}
	log := logx.New(logOut, cfg.Log).With("service", "api", "command", "identity rekey")
	slog.SetDefault(log)
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		return err
	}
	rep, err := identityrekey.Run(ctx, identityrekey.Deps{Pool: pool, Clock: clock.System, Secrets: secrets, River: ins,
		Log: log}, *dryRun)
	return reportRekey(out, rep, err)
}

// reportRekey — вывод команды по исходу прогона: прерван (runErr) — частичный итог, если прогон начался (оператор
// видит, сколько успели, и уже найденные нечитаемые строки), затем ошибка с подсказкой; иначе — полный итог.
func reportRekey(out io.Writer, r identityrekey.Report, runErr error) error {
	if runErr == nil {
		return printRekey(out, r)
	}
	if r.CurrentKey != "" {
		printRekeyPartial(out, r)
	}
	return fmt.Errorf("identity rekey: прогон прерван (повторите — перешифрованное не трогается): %w", runErr)
}

// printRekeyPartial — итог прерванного прогона: только то, что успели; «после» и резервные коды не посчитаны, совета
// про старые ключи нет.
func printRekeyPartial(out io.Writer, r identityrekey.Report) {
	fmt.Fprintf(out, "текущий ключ %s\n", r.CurrentKey)
	fmt.Fprintln(out, "прогон прерван — итог неполный")
	verb := "перешифровано до обрыва"
	if r.DryRun {
		verb = "было бы перешифровано до обрыва (--dry-run)"
	}
	fmt.Fprintf(out, "%s: секретов TOTP %d, refresh-токенов Apple %d\n", verb, r.MFA, r.Apple)
	fmt.Fprintf(out, "секреты TOTP по ключам: до %s\n", byKey(r.MFABefore))
	fmt.Fprintf(out, "refresh-токены Apple по ключам: до %s\n", byKey(r.AppleBefore))
	fmt.Fprintf(out, "нечитаемые секреты TOTP (id аккаунтов): %s\n", idList(r.UnreadableMFA))
	fmt.Fprintf(out, "нечитаемые refresh-токены Apple (id привязок): %s\n", idList(r.UnreadableApple))
}

// printRekey — итог ротации в вывод команды; нечитаемые строки — ошибка (код выхода 1) с подсказкой.
func printRekey(out io.Writer, r identityrekey.Report) error {
	fmt.Fprintf(out, "текущий ключ %s\n", r.CurrentKey)
	if r.DryRun {
		fmt.Fprintln(out, "пробный прогон (--dry-run): ничего не записано")
		fmt.Fprintf(out, "было бы перешифровано: секретов TOTP %d, refresh-токенов Apple %d\n", r.MFA, r.Apple)
	} else {
		fmt.Fprintf(out, "перешифровано: секретов TOTP %d, refresh-токенов Apple %d\n", r.MFA, r.Apple)
	}
	fmt.Fprintf(out, "секреты TOTP по ключам: до %s", byKey(r.MFABefore))
	if !r.DryRun {
		fmt.Fprintf(out, "; после %s", byKey(r.MFAAfter))
	}
	fmt.Fprintf(out, "\nrefresh-токены Apple по ключам: до %s", byKey(r.AppleBefore))
	if !r.DryRun {
		fmt.Fprintf(out, "; после %s", byKey(r.AppleAfter))
	}
	fmt.Fprintf(out, "\nрезервные коды под старыми ключами: %s\n", byKey(r.RecoveryOldKeys))
	fmt.Fprintf(out, "нечитаемые секреты TOTP (id аккаунтов): %s\n", idList(r.UnreadableMFA))
	fmt.Fprintf(out, "нечитаемые refresh-токены Apple (id привязок): %s\n", idList(r.UnreadableApple))
	switch {
	case len(r.UnreadableMFA)+len(r.UnreadableApple) > 0:
		return errors.New("identity rekey: есть нечитаемые строки — их ключ убран из API_IDENTITY_SECRET_KEYS раньше " +
			"rekey: верните его в кольцо (не первым) и повторите rekey")
	case r.DryRun:
	case r.Clean():
		fmt.Fprintln(out, "по числам старые ключи можно убрать из API_IDENTITY_SECRET_KEYS и WORKER_IDENTITY_SECRET_KEYS — "+
			"но не раньше 7 дней после ротации (коды из писем и журнал безопасности под старым ключом)")
	default:
		fmt.Fprintln(out, "старые ключи убирать нельзя: секреты или живые резервные коды ещё под ними "+
			"(резервные коды игроки перевыпускают сами; повторите rekey позже)")
	}
	return nil
}

// byKey — «ключ=число» по алфавиту ключей; пусто — «нет».
func byKey(m map[string]int) string {
	if len(m) == 0 {
		return "нет"
	}
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func idList[T fmt.Stringer](list []T) string {
	if len(list) == 0 {
		return "нет"
	}
	s := make([]string, len(list))
	for i, v := range list {
		s[i] = v.String()
	}
	return strings.Join(s, ", ")
}
