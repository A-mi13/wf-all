import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { ApiError, isApiError } from '../../api/client';
import { Button, Form, FormError, TextField } from '../../ui';
import { cleanAccessReason, reasonProblem } from '../captains/reason';
import type { PlayerLookup } from './queries';
import styles from './players.module.css';

type Values = { email: string; reason: string };

// Предел заголовка почты (контракт LookupEmail): после декодирования — не длиннее 254 байт UTF-8.
const EMAIL_MAX_BYTES = 254;
const bytes = (v: string) => new TextEncoder().encode(v).length;

// Ошибка поиска по полю заголовка (422/400 validation.failed) → поле формы; Form раскладывает только body.*.
const HEADER_FIELDS: ReadonlyMap<string, keyof Values> = new Map<string, keyof Values>([
  ['header.X-WF-Lookup-Email', 'email'],
  ['header.X-WF-Access-Reason', 'reason'],
]);
const headerField = (field: string) => HEADER_FIELDS.get(field);

// Форма поиска игрока по почте (план I-4, Task 9; R19, R41): почта и причина (3–200 символов, однострочная, та же
// проверка, что у контактов заявителя — captains/reason.ts; подсказка «не указывайте ПД игрока» — причина в журнале
// неизменяема). Значения — только в памяти формы: без черновиков (useDraft) и адреса. Ошибка поиска — под формой
// (404 — «не найден», прочее — текстом кода), поля заголовков — у полей. footer — кнопка «Найти» снаружи формы
// (подвал окна, formId); без него — своя кнопка в конце формы (страница).
export function LookupForm({
  lookup,
  formId,
  footer = false,
  onDirty,
}: {
  lookup: PlayerLookup;
  formId?: string;
  footer?: boolean;
  // признак «введено» для dirty окна (закрытие с введённым спрашивает); при размонтировании — false
  onDirty?: (dirty: boolean) => void;
}) {
  const t = useTranslations('players.lookup');
  const form = useForm<Values>({ defaultValues: { email: '', reason: '' } });
  const { query, busy, asked } = lookup;
  const error = asked && query.isError && !busy ? query.error : null;
  const { isDirty } = form.formState;
  useEffect(() => onDirty?.(isDirty), [isDirty, onDirty]);
  useEffect(() => () => onDirty?.(false), [onDirty]);

  // поля заголовков из ответа сервера — к полям формы (фокус — на первое)
  useEffect(() => {
    if (!isApiError(error) || error.code !== 'validation.failed') return;
    let focused = false;
    for (const f of error.fields ?? []) {
      const name = headerField(f.field);
      if (!name) continue;
      form.setError(name, { type: f.code }, { shouldFocus: !focused });
      focused = true;
    }
  }, [error, form]);
  const fieldsOnly =
    isApiError(error) &&
    error.code === 'validation.failed' &&
    (error.fields ?? []).length > 0 &&
    (error.fields ?? []).every((f) => headerField(f.field) !== undefined);

  return (
    <Form
      id={formId}
      form={form}
      showError={false}
      className={styles.form}
      onSubmit={(v) => lookup.search(v.email.trim(), cleanAccessReason(v.reason))}
    >
      <TextField
        name="email"
        type="email"
        label={t('email')}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        autoFocus
        readOnly={busy}
        rules={{
          validate: {
            required: (v: string) => v.trim() !== '',
            maxLength: (v: string) => bytes(v.trim()) <= EMAIL_MAX_BYTES,
          },
        }}
      />
      <TextField
        name="reason"
        label={t('reason')}
        hint={t('reason_hint')}
        autoComplete="off"
        spellCheck={false}
        readOnly={busy}
        rules={{
          validate: {
            format: (v: string) =>
              reasonProblem(cleanAccessReason(v)) !== 'format' || t('reason_invalid'),
            minLength: (v: string) => reasonProblem(cleanAccessReason(v)) !== 'minLength',
            maxLength: (v: string) => reasonProblem(cleanAccessReason(v)) !== 'maxLength',
          },
        }}
      />
      {error && !fieldsOnly && (
        <FormError
          error={isApiError(error) ? error : new ApiError({ status: 0, code: 'internal' })}
        />
      )}
      {!footer && (
        <div className={styles.actions}>
          <Button type="submit" loading={busy}>
            {t('submit')}
          </Button>
        </div>
      )}
    </Form>
  );
}
