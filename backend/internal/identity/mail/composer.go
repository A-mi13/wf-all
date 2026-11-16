// Package mail — письма identity I-2 для воркера (спека identity §8.2; план I-2, Task 19): по одному
// mail.Composer платформы на каждый вид app.MailKinds(). Composer по ref задачи находит адрес и язык получателя
// (адресов в аргументах задач нет) и собирает текст из backend/locales (identity.mail.<письмо>.{subject,text}).
// Только чтение под ролью worker (§5.5): сборка не пишет базу, повтор задачи собирает то же письмо (доставка «хотя
// бы раз»). Коды и адреса — только в теле письма: в ошибки и логи не попадают.
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	pmail "wf/backend/internal/platform/mail"
)

// ComposerDeps — зависимости писем: пул роли worker, кольцо секретов identity (коды — Open(code_enc), адреса
// журнала — Open(meta_enc)), серверные локали и часы (срок кода).
type ComposerDeps struct {
	Pool    *pgxpool.Pool
	Secrets *keys.Ring
	Catalog *i18n.Catalog
	Clock   clock.Clock
}

// mailTime — время в письмах: UTC с подписью (часовой пояс игрока письма не знают).
const mailTime = "2006-01-02 15:04 UTC"

// Register добавляет в реестр воркера Composer каждого вида письма I-2. Без пула, кольца, каталога или часов —
// ошибка сборки (письма с кодами без кольца не собрать, §8.2).
func Register(r *pmail.Registry, d ComposerDeps) error {
	if d.Pool == nil || d.Secrets == nil || d.Catalog == nil || d.Clock == nil {
		return errors.New("identity: письма: нужны пул, кольцо секретов, каталог локалей и часы")
	}
	c := &composers{d: d, q: identitydb.New(d.Pool)}
	code := func(p identitydb.AuthCodePurpose, decoy bool) composeFunc {
		return func(ctx context.Context, kind string, ref uuid.UUID) (pmail.Message, error) {
			return c.code(ctx, kind, ref, p, decoy)
		}
	}
	event := func(kinds ...domain.SecurityKind) composeFunc {
		return func(ctx context.Context, kind string, ref uuid.UUID) (pmail.Message, error) {
			return c.event(ctx, kind, ref, kinds)
		}
	}
	table := map[string]composeFunc{
		app.MailSignupCode:               code(identitydb.AuthCodePurposeSignup, false),
		app.MailSignupAccountExists:      code(identitydb.AuthCodePurposeSignup, true),
		app.MailPasswordResetCode:        code(identitydb.AuthCodePurposePasswordReset, false),
		app.MailEmailChangeCode:          code(identitydb.AuthCodePurposeEmailChange, false),
		app.MailEmailChangeTaken:         code(identitydb.AuthCodePurposeEmailChange, true),
		app.MailEmailChanged:             event(domain.SecEmailChanged),
		app.MailPasswordChanged:          event(domain.SecPasswordChanged, domain.SecPasswordReset),
		app.MailSessionRevokedSuspicious: event(domain.SecRefreshReuse),
		app.MailMfaEnabled:               event(domain.SecMfaEnabled),
		app.MailMfaDisabled:              event(domain.SecMfaDisabled),
		app.MailRecoveryCodeUsed:         event(domain.SecRecoveryCodeUsed),
		app.MailRecoveryCodesRegenerated: event(domain.SecRecoveryCodesRegenerated),
		app.MailMfaFailures:              event(domain.SecMfaFailures),
		app.MailAccountDeletionScheduled: event(domain.SecAccountDeletionScheduled),
		app.MailAccountRestored:          event(domain.SecAccountRestored),
		app.MailAccountDeleted:           event(domain.SecAccountDeleted),
		app.MailMfaResetScheduled:        c.mfaReset,
		app.MailMfaResetCancelled:        c.mfaReset,
		app.MailMfaResetDone:             c.mfaReset,
	}
	for _, kind := range app.MailKinds() {
		fn, ok := table[kind]
		if !ok {
			return fmt.Errorf("identity: письмо %s без Composer", kind)
		}
		if err := r.Add(kind, composer{kind: kind, fn: fn}); err != nil {
			return err
		}
	}
	return nil
}

type composeFunc func(ctx context.Context, kind string, ref uuid.UUID) (pmail.Message, error)

// composer — mail.Composer одного вида.
type composer struct {
	kind string
	fn   composeFunc
}

func (c composer) Compose(ctx context.Context, ref uuid.UUID) (pmail.Message, error) {
	return c.fn(ctx, c.kind, ref)
}

type composers struct {
	d ComposerDeps
	q *identitydb.Queries
}

// skip — письмо больше не нужно: задача отменяется без повторов. why — без адресов и кодов.
func skip(why string) error { return fmt.Errorf("%w: %s", pmail.ErrSkip, why) }

// code — письма по auth_codes.id: настоящий код (decoy = false) или приманка (decoy = true) назначения purpose.
// Строки нет, назначение или приманка не те, код погашен, отозван или истёк (ровно срок — истёк) — ErrSkip; код не
// прочитать (ключ убран из кольца) — ErrSkip: повторы этого не исправят. Приманка сброса пароля (m12) — ErrSkip по
// несовпадению decoy; код сброса аккаунту в бане или с почтой ≠ адресу кода — ErrSkip (как resetBlocked). Адрес — почта строки кода (новая почта у регистрации и смены почты), язык — users.locale
// аккаунта (регистрация — язык из данных регистрации, приманки — язык владельца адреса).
func (c *composers) code(ctx context.Context, kind string, ref uuid.UUID, purpose identitydb.AuthCodePurpose, decoy bool) (pmail.Message, error) {
	row, err := c.q.MailCode(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return pmail.Message{}, skip("кода нет")
	}
	if err != nil {
		return pmail.Message{}, fmt.Errorf("identity: письмо %s: код: %w", kind, err)
	}
	if row.Purpose != purpose || row.Decoy != decoy {
		return pmail.Message{}, skip("код другого вида")
	}
	if row.ConsumedAt != nil || row.RevokedAt != nil || !c.d.Clock.Now().Before(row.ExpiresAt) {
		return pmail.Message{}, skip("код погашен или истёк")
	}
	args := map[string]any{}
	if !decoy {
		if row.CodeEnc == nil || row.KeyID == nil {
			return pmail.Message{}, skip("кода нет в строке")
		}
		plain, err := c.d.Secrets.Open(row.CodeEnc, *row.KeyID, row.ID[:])
		if err != nil {
			return pmail.Message{}, skip("код не прочитать: " + err.Error())
		}
		args["code"] = string(plain)
		args["minutes"] = int(ceilUnits(row.ExpiresAt.Sub(c.d.Clock.Now()), time.Minute))
	}
	name := mailName(kind)
	var locale string
	switch {
	case purpose == identitydb.AuthCodePurposeSignup && !decoy:
		locale = c.signupLocale(row)
	case decoy:
		// приманка — владельцу адреса строки: его язык; аккаунта с этим адресом больше нет (сменил почту или
		// анонимизирован — у deleted почта-заглушка deleted+<id>@invalid, CHECK users_email_format) — письмо не о чем
		owner, err := c.q.MailUserByEmail(ctx, row.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return pmail.Message{}, skip("владельца адреса нет")
		}
		if err != nil {
			return pmail.Message{}, fmt.Errorf("identity: письмо %s: владелец адреса: %w", kind, err)
		}
		locale = mailLocale(owner.Locale)
		if purpose == identitydb.AuthCodePurposeSignup && owner.Status == identitydb.UserStatusPendingDeletion {
			// регистрация на почту аккаунта в периоде удаления (§3.11): «войдите, чтобы отменить удаление»
			return c.compose(row.Email, locale, name, "text_pending", args)
		}
	default:
		if row.UserID == nil {
			return pmail.Message{}, skip("код без аккаунта")
		}
		u, err := c.q.MailUserByID(ctx, *row.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return pmail.Message{}, skip("аккаунта нет")
		}
		if err != nil {
			return pmail.Message{}, fmt.Errorf("identity: письмо %s: аккаунт: %w", kind, err)
		}
		if u.Status == identitydb.UserStatusDeleted {
			return pmail.Message{}, skip("аккаунт удалён")
		}
		// сброс кодом этому аккаунту уже не сменит пароль (как resetBlocked app: бан или почта сменилась, I2) —
		// письмо с кодом не нужно
		if purpose == identitydb.AuthCodePurposePasswordReset && (u.Status == identitydb.UserStatusBanned || u.Email != row.Email) {
			return pmail.Message{}, skip("сброс заблокирован")
		}
		locale = mailLocale(u.Locale)
	}
	return c.compose(row.Email, locale, name, "text", args)
}

// ceilUnits — d в единицах unit с округлением вверх, не меньше 1 (срок кода в письме).
func ceilUnits(d, unit time.Duration) int64 {
	n := int64((d + unit - 1) / unit)
	return max(n, 1)
}

// signupLocale — язык письма регистрации: поле locale данных регистрации (payload_enc, aad = id кода); не прочитать —
// язык по умолчанию (код важнее языка). Хэш пароля из тех же данных не разбирается и не хранится.
func (c *composers) signupLocale(row identitydb.MailCodeRow) string {
	if row.PayloadEnc == nil || row.KeyID == nil {
		return i18n.Default
	}
	raw, err := c.d.Secrets.Open(row.PayloadEnc, *row.KeyID, row.ID[:])
	if err != nil {
		return i18n.Default
	}
	var p struct {
		Locale string `json:"locale"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return i18n.Default
	}
	return mailLocale(p.Locale)
}

// eventMeta — поля meta журнала, которые нужны письмам (без ПД).
type eventMeta struct {
	Left     *int   `json:"left"`
	Failures *int   `json:"failures"`
	PurgeAt  string `json:"purge_at"`
}

// event — письма по security_events.id. Записи нет (анонимизация стирает журнал) или вид записи не из kinds —
// ErrSkip. Получатель анонимизирован (deleted) — ErrSkip, кроме account_deleted: его адрес — meta_enc новой записи
// анонимизации (I3), как и старая почта email_changed; meta_enc стёрт чисткой — ErrSkip. Остальные — users.email.
// account_deletion_scheduled — только пока аккаунт pending_deletion; нет нужного ключа meta — ошибка, не ErrSkip.
func (c *composers) event(ctx context.Context, kind string, ref uuid.UUID, kinds []domain.SecurityKind) (pmail.Message, error) {
	row, err := c.q.MailSecurityEvent(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return pmail.Message{}, skip("записи журнала нет")
	}
	if err != nil {
		return pmail.Message{}, fmt.Errorf("identity: письмо %s: журнал: %w", kind, err)
	}
	if !slices.Contains(kinds, domain.SecurityKind(row.Kind)) {
		return pmail.Message{}, skip("запись журнала другого вида")
	}
	deleted := row.Status == identitydb.UserStatusDeleted
	if deleted != (kind == app.MailAccountDeleted) {
		return pmail.Message{}, skip("статус аккаунта не тот")
	}
	// River не гарантирует порядок: удаление успели отменить, пока письмо в очереди, — «удаление назначено» не нужно
	if kind == app.MailAccountDeletionScheduled && row.Status != identitydb.UserStatusPendingDeletion {
		return pmail.Message{}, skip("удаление уже не назначено")
	}
	to := row.Email
	if kind == app.MailEmailChanged || kind == app.MailAccountDeleted {
		if row.MetaEnc == nil || row.KeyID == nil {
			return pmail.Message{}, skip("адрес стёрт")
		}
		plain, err := c.d.Secrets.Open(row.MetaEnc, *row.KeyID, ref[:])
		if err != nil {
			return pmail.Message{}, skip("адрес не прочитать: " + err.Error())
		}
		to = string(plain)
	}
	var meta eventMeta
	if err := json.Unmarshal(row.Meta, &meta); err != nil {
		return pmail.Message{}, fmt.Errorf("identity: письмо %s: meta журнала: %w", kind, err)
	}
	// нет ключа meta, который пишет производитель, — расхождение кода, а не «письмо не нужно»: обычная ошибка (повторы,
	// затем видимая мёртвая задача), не ErrSkip
	args := map[string]any{"time": row.CreatedAt.UTC().Format(mailTime)}
	switch kind {
	case app.MailRecoveryCodeUsed:
		if meta.Left == nil {
			return pmail.Message{}, fmt.Errorf("identity: письмо %s: в meta журнала нет left", kind)
		}
		args["left"] = *meta.Left
	case app.MailMfaFailures:
		if meta.Failures == nil {
			return pmail.Message{}, fmt.Errorf("identity: письмо %s: в meta журнала нет failures", kind)
		}
		args["failures"] = *meta.Failures
	case app.MailAccountDeletionScheduled:
		purge, err := time.Parse(time.RFC3339, meta.PurgeAt)
		if err != nil {
			return pmail.Message{}, fmt.Errorf("identity: письмо %s: purge_at в meta журнала: %w", kind, err)
		}
		args["purge"] = purge.UTC().Format(mailTime)
	}
	return c.compose(to, mailLocale(row.Locale), mailName(kind), "text", args)
}

// mfaReset — письма по mfa_resets.id (роль worker: SELECT mfa_resets и users). Назначен — пока сброс живой,
// отменён и выполнен — по исходу; записи нет или аккаунт анонимизирован — ErrSkip.
func (c *composers) mfaReset(ctx context.Context, kind string, ref uuid.UUID) (pmail.Message, error) {
	row, err := c.q.MailMfaReset(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return pmail.Message{}, skip("сброса нет")
	}
	if err != nil {
		return pmail.Message{}, fmt.Errorf("identity: письмо %s: сброс 2FA: %w", kind, err)
	}
	if row.Status == identitydb.UserStatusDeleted {
		return pmail.Message{}, skip("аккаунт удалён")
	}
	args := map[string]any{}
	switch kind {
	case app.MailMfaResetScheduled:
		if row.ExecutedAt != nil || row.CancelledAt != nil {
			return pmail.Message{}, skip("сброс уже закрыт")
		}
		args["execute"] = row.ExecuteAt.UTC().Format(mailTime)
	case app.MailMfaResetCancelled:
		if row.CancelledAt == nil {
			return pmail.Message{}, skip("сброс не отменён")
		}
		by := "staff"
		if row.CancelledBy != nil && (*row.CancelledBy == "owner" || *row.CancelledBy == "system") {
			by = *row.CancelledBy
		}
		args["by"], args["time"] = by, row.CancelledAt.UTC().Format(mailTime)
	case app.MailMfaResetDone:
		if row.ExecutedAt == nil {
			return pmail.Message{}, skip("сброс не выполнен")
		}
		args["time"] = row.ExecutedAt.UTC().Format(mailTime)
	}
	return c.compose(row.Email, mailLocale(row.Locale), mailName(kind), "text", args)
}

// compose — письмо identity.mail.<name>.subject и .<text> с подписью mail.footer.
func (c *composers) compose(to, locale, name, text string, args map[string]any) (pmail.Message, error) {
	subject, err := c.d.Catalog.Text(locale, "identity.mail."+name+".subject", args)
	if err != nil {
		return pmail.Message{}, err
	}
	body, err := c.d.Catalog.Text(locale, "identity.mail."+name+"."+text, args)
	if err != nil {
		return pmail.Message{}, err
	}
	footer, err := c.d.Catalog.Text(locale, "mail.footer", nil)
	if err != nil {
		return pmail.Message{}, err
	}
	return pmail.Message{To: to, Subject: subject, Text: fmt.Sprintf("%s\n\n%s\n", body, footer)}, nil
}

// mailName — вид без префикса модуля: identity.signup_code → signup_code.
func mailName(kind string) string { return strings.TrimPrefix(kind, "identity.") }

// mailLocale — язык письма из users.locale: первичный подтег в нижнем регистре; не из i18n.Supported —
// i18n.Default.
func mailLocale(locale string) string {
	primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(locale)), "-")
	primary, _, _ = strings.Cut(primary, "_")
	if slices.Contains(i18n.Supported, primary) {
		return primary
	}
	return i18n.Default
}
