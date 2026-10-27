// Command admin-api — API админки. Отдельный бинарник, домен и порт. Без аргументов — сервер (HTTP и фон:
// письма staff.mail очереди staff, чистка, периодика платформы при лидерстве в River); с аргументами staff … —
// команды оператора (спека сотрудников §4.2, §4.11): HTTP не поднимают, письма ставят в очередь staff —
// отправит работающий сервер.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/server"
	"wf/backend/internal/staff"
	staffadmin "wf/backend/internal/staff/admin"
	"wf/backend/internal/staff/jobs"
	"wf/backend/locales"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		// stderr попадает в логи хостинга: почта (ввод оператора) — маской
		fmt.Fprintln(os.Stderr, "admin-api:", logx.Mask(err.Error()))
		if errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, usage)
		}
		os.Exit(exitCode(err))
	}
}

// wrapSender — отправитель писем фона поверх SMTP; тест подменяет его, чтобы держать письмо «в полёте» и видеть
// отмену его ctx (SMTP-клиент отмену посреди DATA не замечает).
var wrapSender = func(s mail.Sender) mail.Sender { return s }

const (
	// staffMailWorkers — одновременных писем очереди staff: писем сотрудникам единицы в час.
	staffMailWorkers = 5
	// softStopTimeout — мягкая остановка River после дренажа HTTP (ADMIN_SHUTDOWN_TIMEOUT, по умолчанию 10 с):
	// 10 + 12 + 5 с жёсткой (queue.Stop) = 27 с — с запасом в 30 с между SIGTERM и SIGKILL (Render). Не уложилось
	// письмо — его ctx отменяется (SMTP обрывает соединение сразу, mail.SMTP), задача вернётся в повтор.
	softStopTimeout = 12 * time.Second
)

// run без аргументов — сервер; staff … — команда оператора. Ошибка использования (errUsage) — код выхода 2.
func run(ctx context.Context, args, environ []string, out, logOut io.Writer) error {
	// команда разбирается первой: опечатка оператора — usage, а не поднятый сервер и не подключение к базе
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load[config.Admin]("ADMIN_", environ)
	if err != nil {
		return err
	}
	// лимиты и доверие к заголовкам проверяются до подключения к базе
	rules, err := ratelimit.ParseRules(cfg.RateLimits, ratelimit.DefaultRules())
	if err != nil {
		return err
	}
	if err := rules.Validate(); err != nil {
		return err
	}
	// BFF у админки нет: её фронт ходит в API напрямую
	pc := peer.Config{TrustedProxies: cfg.TrustedProxies, ClientIPHeader: cfg.ClientIPHeader}
	if err := pc.Validate(); err != nil {
		return err
	}
	if err := admin.ValidateOrigin(cfg.Origin); err != nil {
		return err
	}
	if err := admin.ValidateCookieSecure(cfg.Origin, cfg.CookieSecure); err != nil {
		return err
	}
	if err := admin.ValidateAllowlist(cfg.IPAllowlist); err != nil {
		return err
	}
	// кольцо ключей сотрудников и тексты писем — до базы: пустое или битое кольцо — отказ старта
	ring, err := keys.NewRing(cfg.StaffSecretKeys)
	if err != nil {
		return fmt.Errorf("ADMIN_STAFF_SECRET_KEYS: %w", err)
	}
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		return err
	}
	if cmd != nil {
		return runCommand(ctx, *cmd, cfg, ring, cat, out, logOut)
	}
	// SMTP — только серверу (письма шлёт фон): битый адрес — отказ старта до базы
	sender, err := mail.NewSMTP(mail.SMTPConfig{Addr: cfg.Mail.SMTPAddr, From: cfg.Mail.From,
		Username: cfg.Mail.Username, Password: cfg.Mail.Password, TLS: cfg.Mail.TLS})
	if err != nil {
		return err
	}
	send := wrapSender(sender)
	return server.RunService(ctx, "admin-api", server.HTTPConfig{Log: cfg.Log, HTTP: cfg.HTTP, DB: cfg.DB}, logOut,
		func(log *slog.Logger, pool *pgxpool.Pool) (server.Service, error) {
			a, err := assemble(log, pool, cfg, ring, cat)
			if err != nil {
				return server.Service{}, err
			}
			h, err := admin.NewHandler(log, admin.Options{
				Docs:           cfg.HTTP.DocsEnabled,
				RequestTimeout: cfg.HTTP.RequestTimeout,
				Peer:           pc,
				DB:             pool,
				Limiter:        a.deps.Limiter,
				RateRules:      rules,
				Origin:         cfg.Origin,
				IPAllowlist:    cfg.IPAllowlist,
				CookieSecure:   cfg.CookieSecure,
				StaticDir:      cfg.StaticDir,
				Sessions:       a.staff.Sessions(),
				Staff:          a.staff,
				// тот же сервис, в котором staff зарегистрировал действия (ревью 10b: два сервиса — approve 500)
				Approvals: a.approvals,
				Geo:       geoadmin.New(geo.New(pool)),
				Clock:     a.deps.Clock,
			})
			if err != nil {
				return server.Service{}, err
			}
			bg, err := background(log, pool, a, send)
			if err != nil {
				return server.Service{}, err
			}
			return server.Service{Handler: h, Background: bg}, nil
		})
}

// assembly — модуль staff на пуле admin-api: зависимости сценариев, хендлер и сервис подтверждений, в котором
// staff зарегистрировал свои действия (одна переменная на всё: Options.Approvals, RegisterApprovals, чистка).
type assembly struct {
	deps      staff.Deps
	staff     *staffadmin.Handler
	approvals *approvals.Service
}

// assemble — сборка staff для сервера и команд оператора. Deps.River — клиент только для вставки: сценарии и
// команды ставят staff.mail в транзакции действия; исполняет письма отдельный клиент фона (у него в
// исполнителях Deps — иначе цикл). Реестр подтверждений проверяется сразу: невалидный — отказ старта.
func assemble(log *slog.Logger, pool *pgxpool.Pool, cfg config.Admin, ring *keys.Ring, cat *i18n.Catalog) (assembly, error) {
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log})
	if err != nil {
		return assembly{}, err
	}
	// argon2id под семафором: не больше одновременных хэширований, чем ядер
	hasher, err := password.NewHasher(password.DefaultParams(), max(1, runtime.GOMAXPROCS(0)))
	if err != nil {
		return assembly{}, err
	}
	d := staff.Deps{
		Pool: pool, Clock: clock.System, Keys: ring, Hasher: hasher, Limiter: ratelimit.NewPG(pool, clock.System),
		River: ins, Rand: rand.Reader, Origin: cfg.Origin, Locales: cat, Log: log,
	}
	h := staffadmin.New(d, staffadmin.Cookie{Name: admin.CookieName(cfg.CookieSecure), Secure: cfg.CookieSecure})
	reg := approvals.NewRegistry()
	svc := approvals.New(pool, d.Clock, reg)
	h.RegisterApprovals(reg, svc)
	if err := reg.Validate(); err != nil {
		return assembly{}, fmt.Errorf("admin-api: реестр подтверждений: %w", err)
	}
	return assembly{deps: d, staff: h, approvals: svc}, nil
}

// background — фон сервера: исполняющий River-клиент (очередь staff — письма; периодика платформы
// periodic.Jobs — если admin-api станет лидером River, чистки воркера не пропадут, P27) и тикер чистки staff.
// River работает на ctx без отмены и останавливается сам после тикера: мягко (письмо в полёте доигрывается),
// не уложился — StopAndCancel, задача вернётся в повтор.
func background(log *slog.Logger, pool *pgxpool.Pool, a assembly, sender mail.Sender) (func(context.Context) error, error) {
	workers := allWorkers(a.deps, sender)
	client, err := river.NewClient(riverpgxv5.New(pool), riverConfig(workers, log))
	if err != nil {
		return nil, err
	}
	cleaner := jobs.NewCleaner(a.deps, a.approvals)
	return func(ctx context.Context) error {
		return runBackground(ctx, client, cleaner, log)
	}, nil
}

func runBackground(ctx context.Context, client *river.Client[pgx.Tx], cleaner *jobs.Cleaner, log *slog.Logger) error {
	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("river: %w", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { cleaner.Run(ctx) })
	log.Info("фон запущен", "queue", queue.Staff)
	<-ctx.Done()
	wg.Wait() // чистка остановлена — порция в полёте откатилась
	return queue.Stop(context.Background(), client, softStopTimeout)
}

// --- команды оператора ---

const usage = "использование: admin-api — сервер; " +
	"admin-api staff bootstrap --email <почта> --name <имя> [--print-code] [--keys-lost] | " +
	"staff invite-resend --email <почта> [--print-code] [--keys-lost] | " +
	"staff reset-access --email <почта> [--print-code] [--keys-lost] | " +
	"staff disable --email <почта> | staff rekey " +
	"(--keys-lost: ключ, на котором лежат секреты базы, потерян безвозвратно — выполнить, несмотря на чужое кольцо)"

// errUsage — неверная команда: код выхода 2 и usage в stderr.
var errUsage = errors.New("неверная команда")

func usageError(msg string) error { return fmt.Errorf("%w: %s", errUsage, msg) }

// exitCode — 2 для ошибки использования, 1 для остальных.
func exitCode(err error) int {
	if errors.Is(err, errUsage) {
		return 2
	}
	return 1
}

// newFlagSet — флаги подкоманды без печати в stderr: ошибку разбора возвращает parseCommand.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("staff "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// command — разобранная подкоманда staff.
type command struct {
	name      string // bootstrap | invite-resend | reset-access | disable | rekey
	email     string
	fullName  string
	printCode bool
	// keysLost — --keys-lost: ни один ключ базы не в кольце, потому что он потерян безвозвратно (не «не то кольцо»)
	keysLost bool
}

// parseCommand — nil без аргументов (сервер). Флаги — только свои у каждой подкоманды; лишние аргументы,
// неизвестная подкоманда или флаг, нет обязательного — ошибка использования.
func parseCommand(args []string) (*command, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if args[0] != "staff" || len(args) < 2 {
		return nil, usageError(fmt.Sprintf("неизвестная команда %q", strings.Join(args, " ")))
	}
	c := &command{name: args[1]}
	fs := newFlagSet(c.name)
	var email, name *string
	var printCode, keysLost *bool
	switch c.name {
	case "bootstrap":
		email, name = fs.String("email", "", ""), fs.String("name", "", "")
		printCode, keysLost = fs.Bool("print-code", false, ""), fs.Bool("keys-lost", false, "")
	case "invite-resend", "reset-access":
		email = fs.String("email", "", "")
		printCode, keysLost = fs.Bool("print-code", false, ""), fs.Bool("keys-lost", false, "")
	case "disable":
		email = fs.String("email", "", "")
	case "rekey":
	default:
		return nil, usageError(fmt.Sprintf("неизвестная подкоманда staff %q", c.name))
	}
	if err := fs.Parse(args[2:]); err != nil {
		return nil, usageError(fmt.Sprintf("staff %s: %v", c.name, err))
	}
	if fs.NArg() > 0 {
		return nil, usageError(fmt.Sprintf("staff %s: лишние аргументы %q", c.name, strings.Join(fs.Args(), " ")))
	}
	if email != nil {
		if c.email = strings.TrimSpace(*email); c.email == "" {
			return nil, usageError(fmt.Sprintf("staff %s: нужен --email", c.name))
		}
	}
	if name != nil {
		if c.fullName = strings.TrimSpace(*name); c.fullName == "" {
			return nil, usageError(fmt.Sprintf("staff %s: нужен --name", c.name))
		}
	}
	if printCode != nil {
		c.printCode = *printCode
	}
	if keysLost != nil {
		c.keysLost = *keysLost
	}
	return c, nil
}

// runCommand — команда оператора: пул ролью admin (ADMIN_DATABASE_URL), сборка staff как у сервера, без HTTP
// и без SMTP. Журнал — logOut (stderr); итог и код (только с --print-code) — out (stdout).
func runCommand(ctx context.Context, c command, cfg config.Admin, ring *keys.Ring, cat *i18n.Catalog, out, logOut io.Writer) error {
	log := logx.New(logOut, cfg.Log).With("service", "admin-api", "command", "staff "+c.name)
	slog.SetDefault(log)
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	a, err := assemble(log, pool, cfg, ring, cat)
	if err != nil {
		return err
	}
	return execCommand(ctx, c, a.staff, out, logOut)
}

// queuedNote — что делать с письмом: команда его не отправляет.
const queuedNote = "письмо поставлено в очередь staff — отправит работающий admin-api (не запущен — письмо ждёт его старта)"

// execCommand — подкоманда на собранном хендлере. Почта в выводе и ошибках — маской (stderr и stdout команды
// попадают в логи хостинга), код приглашения — только с --print-code и только в out.
func execCommand(ctx context.Context, c command, h *staffadmin.Handler, out, errOut io.Writer) error {
	who := logx.Mask(c.email)
	if c.name == "bootstrap" || c.name == "invite-resend" || c.name == "reset-access" {
		if err := checkKeys(ctx, h, c.keysLost, errOut); err != nil {
			return err
		}
	}
	if c.keysLost {
		ctx = staffadmin.WithKeysLost(ctx) // пометка keys_lost в записи аудита команды
	}
	switch c.name {
	case "bootstrap":
		code, err := h.OperatorBootstrap(ctx, c.email, c.fullName, c.printCode)
		if err != nil {
			return hint("bootstrap", err)
		}
		fmt.Fprintf(out, "владелец %s заведён (статус invited), приглашение действует 24 ч; активным владельцам — письмо staff.bootstrapped\n", who)
		printInvite(out, code)
	case "invite-resend":
		code, err := h.OperatorInviteResend(ctx, c.email, c.printCode)
		if err != nil {
			return hint("invite-resend", err)
		}
		fmt.Fprintf(out, "%s: новый код приглашения, прежний недействителен; срок — 24 ч\n", who)
		printInvite(out, code)
	case "reset-access":
		code, err := h.OperatorResetAccess(ctx, c.email, c.printCode)
		if err != nil {
			return hint("reset-access", err)
		}
		fmt.Fprintf(out, "%s: доступ сброшен (статус invited, сессии завершены, второй фактор удалён); код действует 24 ч\n", who)
		printInvite(out, code)
	case "disable":
		if err := h.OperatorDisable(ctx, c.email); err != nil {
			return hint("disable", err)
		}
		fmt.Fprintf(out, "%s: учётка выключена, сессии завершены\n", who)
	case "rekey":
		rep, err := h.OperatorRekey(ctx)
		printRekey(out, rep)
		if err != nil {
			return fmt.Errorf("staff rekey: прогон прерван (повторите — перешифрованное не трогается): %w", err)
		}
		if len(rep.UnreadableMFA)+len(rep.UnreadableInvites) > 0 {
			return errors.New("staff rekey: есть нечитаемые строки — их ключ убран из ADMIN_STAFF_SECRET_KEYS раньше rekey: " +
				"верните его в кольцо (не первым) и повторите rekey; приглашения лечит «Прислать код ещё раз» или invite-resend, " +
				"секрет TOTP — reset-access")
		}
	}
	return nil
}

// printInvite — письмо в очереди; код — только если его отдали (--print-code).
func printInvite(out io.Writer, code string) {
	fmt.Fprintln(out, queuedNote)
	if code != "" {
		fmt.Fprintf(out, "код приглашения: %s\n", code)
	}
}

// printRekey — все счётчики итога и id нечитаемых строк (M-1b).
func printRekey(out io.Writer, r staffadmin.RekeyReport) {
	fmt.Fprintf(out, "перешифровано текущим ключом: секретов TOTP %d, кодов приглашений %d\n", r.MFA, r.Invites)
	fmt.Fprintf(out, "ещё на старых ключах: секретов TOTP %d, кодов приглашений %d, резервных кодов %d\n",
		r.MFAOnOldKeys, r.InvitesOnOldKeys, r.RecoveryOnOldKeys)
	fmt.Fprintf(out, "нечитаемые секреты TOTP (id сотрудников): %s\n", ids(r.UnreadableMFA))
	fmt.Fprintf(out, "нечитаемые коды приглашений (id приглашений): %s\n", ids(r.UnreadableInvites))
	if r.Clean() {
		fmt.Fprintln(out, "старые ключи можно убрать из ADMIN_STAFF_SECRET_KEYS")
	} else {
		fmt.Fprintln(out, "старые ключи убирать нельзя: нужны нули во всех счётчиках и пустые списки нечитаемых "+
			"(резервные коды на старом ключе — сотрудники перевыпускают их сами)")
	}
}

func ids[T fmt.Stringer](list []T) string {
	if len(list) == 0 {
		return "нет"
	}
	s := make([]string, len(list))
	for i, v := range list {
		s[i] = v.String()
	}
	return strings.Join(s, ", ")
}

// hint — ошибка сценария с подсказкой оператору; исходная ошибка — в цепочке (errors.Is).
func hint(cmd string, err error) error {
	var msg string
	switch {
	case errors.Is(err, staff.ErrEmailTaken):
		msg = "почта уже занята (учётка в любом статусе): истёк код — invite-resend, потерян доступ — reset-access"
	case errors.Is(err, staff.ErrNotFound):
		msg = "учётки с такой почтой нет"
	case errors.Is(err, staff.ErrLastOwner):
		msg = "это последний активный владелец — без него админкой некому управлять: сначала заведите второго " +
			"(admin-api staff bootstrap) и дождитесь, пока он войдёт"
	case errors.Is(err, staff.ErrWrongStage) && cmd == "disable":
		msg = "учётка уже выключена"
	case errors.Is(err, staff.ErrWrongStage):
		msg = "учётка не ждёт приглашения (не invited): потерян доступ — reset-access"
	default:
		return fmt.Errorf("staff %s: %w", cmd, err)
	}
	return fmt.Errorf("staff %s: %s: %w", cmd, msg, err)
}

// realWorkers — исполнители admin-api; их виды — ровно queue.AdminKinds (сверка — TestAdminKinds).
func realWorkers(d staff.Deps, sender mail.Sender) *river.Workers {
	workers := river.NewWorkers()
	jobs.AddWorkers(workers, d, sender)
	return workers
}

// allWorkers — реестр исполняющего клиента admin-api: свои исполнители и заглушки видов воркера (I1 ревью 10c): став
// лидером River, рескьюер admin-api вернёт зависшую задачу воркера в retryable, а не выбросит неизвестный вид.
func allWorkers(d staff.Deps, sender mail.Sender) *river.Workers {
	workers := realWorkers(d, sender)
	queue.AddForeign(workers, queue.WorkerKinds())
	return workers
}

// checkKeys — сверка ключей базы с кольцом команды, шифрующей код приглашения (отказ 1 ревью 10c, I-r1):
//   - в базе есть ключи и НИ ОДИН не в кольце — «не то кольцо» (другая машина, старый .env): отказ до записи, код 1 —
//     иначе команда погасила бы живое приглашение и выпустила код на ключе, которого нет у сервера;
//   - часть ключей не в кольце — предупреждение в stderr, команда выполняется: это документированный выход при
//     потерянном ключе (reset-access, invite-resend выпускают новый код текущим ключом);
//   - ни один не в кольце, но оператор подтвердил --keys-lost (единственный ключ потерян безвозвратно — по базе это
//     неотличимо от «не того кольца», I-r2) — то же предупреждение, команда выполняется; кольцо и ключи не меняются.
//
// Чего сверка не видит: пустую базу и лишний (новый) ключ в кольце оператора — кольцо команды должно совпадать с
// кольцом сервера, порядок тоже. Ключи и их id не печатаются — только число.
func checkKeys(ctx context.Context, h *staffadmin.Handler, keysLost bool, errOut io.Writer) error {
	known, unknown, err := h.OperatorKeyCheck(ctx)
	if err != nil {
		return err
	}
	switch {
	case unknown > 0 && known == 0 && !keysLost:
		return fmt.Errorf("ни один ключ, на котором лежат секреты сотрудников в базе, не входит в ADMIN_STAFF_SECRET_KEYS "+
			"этой команды (ключей базы: %d) — кольцо не то: запустите команду с кольцом сервера (все его ключи, тот же порядок); "+
			"если ключ потерян безвозвратно — повторите с --keys-lost (с не тем кольцом флаг выпустит код, который сервер "+
			"не прочтёт; сверьте кольцо с переменной сервиса)", unknown)
	case unknown > 0:
		fmt.Fprintf(errOut, "предупреждение: секреты сотрудников (часть или все) лежат на ключах, которых нет в ADMIN_STAFF_SECRET_KEYS "+
			"этой команды (ключей: %d) — если ключ потерян, это ожидаемо (reset-access и invite-resend выпускают новый код "+
			"текущим ключом); если нет — проверьте, что кольцо команды совпадает с кольцом сервера\n", unknown)
	}
	return nil
}

// riverConfig — исполняющий клиент admin-api: обслуживание — общее с воркером (queue.ClientConfig от
// queue.MaintenanceConfig, страж TestAdminRiverMaintenance), очередь — только staff, периодика — общий список платформы.
func riverConfig(workers *river.Workers, log *slog.Logger) *river.Config {
	return queue.ClientConfig(staffQueues(), workers, periodic.Jobs(), log)
}

// staffQueues — очереди исполняющего клиента admin-api: только staff (у воркера её нет — TestAdminKindsQueues).
func staffQueues() map[string]river.QueueConfig {
	return map[string]river.QueueConfig{queue.Staff: {MaxWorkers: staffMailWorkers}}
}
