import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Form } from './Form';
import { TextField } from './TextField';

// Текстовое поле в форме: пустое с подсказкой, заполненное, с ошибкой, недоступное.
export default { title: 'UI/Form/TextField', visual: true } satisfies StoryMeta;

function Sample({ invalid, disabled }: { invalid?: boolean; disabled?: boolean }) {
  const t = useTranslations('staff.invite');
  const form = useForm({ defaultValues: { email: invalid ? 'olga.v@' : '', name: 'Ольга' } });
  useEffect(() => {
    if (invalid) form.setError('email', { type: 'pattern' });
  }, [form, invalid]);
  return (
    <Form form={form} onSubmit={() => undefined} className={styles.column}>
      <TextField name="email" type="email" label={t('email')} disabled={disabled} />
      <TextField name="name" label={t('name')} hint={t('name_hint')} disabled={disabled} />
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Error: Story = () => <Sample invalid />;
export const Disabled: Story = () => <Sample disabled />;
