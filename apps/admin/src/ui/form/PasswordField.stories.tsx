import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Form } from './Form';
import { PasswordField } from './PasswordField';

// Поле пароля с «Показать/Скрыть»: с подсказкой требований, с ошибкой, недоступное.
export default { title: 'UI/Form/PasswordField', visual: true } satisfies StoryMeta;

function Sample({ invalid, disabled }: { invalid?: boolean; disabled?: boolean }) {
  const t = useTranslations('me.password');
  // образец пароля — данные поля, не текст интерфейса
  const form = useForm({ defaultValues: { current: 'football-2026', next: '' } });
  useEffect(() => {
    if (invalid) form.setError('next', { type: 'too_short' });
  }, [form, invalid]);
  return (
    <Form form={form} onSubmit={() => undefined} className={styles.column}>
      <PasswordField name="current" label={t('current')} disabled={disabled} />
      <PasswordField
        name="next"
        label={t('new')}
        hint={t('new_hint')}
        autoComplete="new-password"
        disabled={disabled}
      />
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Error: Story = () => <Sample invalid />;
export const Disabled: Story = () => <Sample disabled />;
