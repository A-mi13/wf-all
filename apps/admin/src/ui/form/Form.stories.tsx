import { useEffect, useId } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { ApiError } from '../../api/client';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Button } from '../Button';
import { OtpInput } from '../OtpInput';
import { FieldError } from './FieldError';
import { Form } from './Form';
import { FormError } from './FormError';
import { TextField } from './TextField';

// Форма целиком: общая ошибка ответа сервера над полями (FormError, текст по коду) и своё поле через
// Controller с FieldError (код второго фактора — как на смене пароля).
export default { title: 'UI/Form/Form', visual: true } satisfies StoryMeta;

function Sample({ failed }: { failed?: boolean }) {
  const t = useTranslations();
  const codeError = useId();
  const form = useForm({ defaultValues: { email: 'olga.v@wf.example', code: '' } });
  useEffect(() => {
    if (failed) form.setError('code', { type: 'required' });
  }, [form, failed]);
  return (
    <Form form={form} onSubmit={() => undefined} showError={false} className={styles.column}>
      {failed && <FormError error={new ApiError({ status: 409, code: 'staff.email_taken' })} />}
      <TextField name="email" type="email" label={t('staff.invite.email')} />
      <Controller
        name="code"
        control={form.control}
        render={({ field, fieldState }) => (
          <OtpInput
            label={t('me.password.code')}
            onComplete={field.onChange}
            onChange={field.onChange}
            invalid={fieldState.invalid}
            describedBy={codeError}
          />
        )}
      />
      <FieldError name="code" id={codeError} />
      <Button type="submit">{t('staff.invite.submit')}</Button>
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Error: Story = () => <Sample failed />;
