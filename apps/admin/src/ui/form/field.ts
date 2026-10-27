import { useCallback, useId } from 'react';
import { type FieldError as RhfFieldError, get, useFormContext } from 'react-hook-form';
import { useTranslations } from 'use-intl';

// Текст ошибки поля: своё сообщение правила (rules: { validate: () => 'текст' }) → ui.field_errors.<тип>
// (правило react-hook-form или код поля validation.failed: required, minLength, format, common…) →
// общий ui.field_errors.invalid.
export function useFieldErrorText(): (error: RhfFieldError) => string {
  const t = useTranslations('ui.field_errors');
  return useCallback(
    (error) => {
      if (typeof error.message === 'string' && error.message) return error.message;
      const type = String(error.type ?? '');
      return type && t.has(type) && typeof t.raw(type) === 'string' ? t(type) : t('invalid');
    },
    [t],
  );
}

// Связки поля: id контрола, подсказки и ошибки; aria-describedby — то, что сейчас на экране.
export function useField(name: string, hasHint: boolean) {
  const id = useId();
  const { formState } = useFormContext();
  const error = get(formState.errors, name) as RhfFieldError | undefined;
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hasHint && hintId, error && errorId].filter(Boolean).join(' ') || undefined;
  return { id, hintId, errorId, error, describedBy, invalid: Boolean(error) };
}
