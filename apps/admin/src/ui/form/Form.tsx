import { type FormHTMLAttributes, type ReactNode, useState } from 'react';
import { type FieldValues, FormProvider, type UseFormReturn } from 'react-hook-form';
import { ApiError, isApiError } from '../../api/client';
import { cx } from '../cx';
import { applyFieldErrors } from './apiErrors';
import { FormErrorContext } from './context';
import styles from './Field.module.css';
import { FormError } from './FormError';

export type FormProps<T extends FieldValues> = Omit<
  FormHTMLAttributes<HTMLFormElement>,
  'onSubmit' | 'onError' | 'children'
> & {
  form: UseFormReturn<T>; // useForm() экрана
  // Отправка: обычно mutation.mutateAsync. Отказ промиса — ошибка формы (ApiError или любая).
  onSubmit: (values: T) => unknown;
  // Ошибка до раскладки: экран сам показывает особые случаи (attempts_left, 429 с таймером) и
  // возвращает true — тогда ни полей, ни FormError форма не трогает.
  onError?: (error: ApiError) => boolean | void;
  // Общий текст ошибки над полями; false — экран ставит <FormError /> внутри формы сам.
  showError?: boolean;
  children: ReactNode;
};

// Форма админки (спека §9.1): react-hook-form + разбор ответа сервера. 400 validation.failed
// раскладывается по полям (body.<поле> → setError), что не легло на поля, и прочие коды — общий
// текст FormError по errors.<код> (useErrorText, откат на errors.internal). Новая отправка
// стирает прошлую общую ошибку. Проверка браузера выключена (noValidate): тексты — наши.
export function Form<T extends FieldValues>({
  form,
  onSubmit,
  onError,
  showError = true,
  className,
  children,
  ...rest
}: FormProps<T>) {
  const [error, setError] = useState<ApiError | null>(null);

  const submit = form.handleSubmit(
    async (values) => {
      setError(null);
      try {
        await onSubmit(values);
      } catch (e) {
        // не ApiError — сбой кода или сети до ответа: пользователю общий текст
        const apiError = isApiError(e) ? e : new ApiError({ status: 0, code: 'internal' });
        if (onError?.(apiError) === true) return;
        if (applyFieldErrors(form, apiError)) return;
        setError(apiError);
      }
    },
    () => setError(null),
  );

  return (
    <FormProvider {...form}>
      <FormErrorContext value={error}>
        <form
          noValidate
          {...rest}
          className={cx(styles.form, className)}
          onSubmit={(e) => void submit(e)}
        >
          {showError && <FormError />}
          {children}
        </form>
      </FormErrorContext>
    </FormProvider>
  );
}
