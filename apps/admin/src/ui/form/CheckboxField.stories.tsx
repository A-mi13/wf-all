import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { CheckboxField } from './CheckboxField';
import { Form } from './Form';

// Флажок формы: снят и отмечен, обязательный не отмечен (ошибка), недоступный.
export default { title: 'UI/Form/CheckboxField', visual: true } satisfies StoryMeta;

function Sample({ invalid, disabled }: { invalid?: boolean; disabled?: boolean }) {
  const t = useTranslations();
  const form = useForm({ defaultValues: { ack: false, low: true } });
  useEffect(() => {
    if (invalid) form.setError('ack', { type: 'required' });
  }, [form, invalid]);
  return (
    <Form form={form} onSubmit={() => undefined} className={styles.column}>
      <CheckboxField name="ack" label={t('auth.setup.saved')} disabled={disabled} />
      <CheckboxField
        name="low"
        label={t('me.codes.title')}
        hint={t('me.codes.low')}
        disabled={disabled}
      />
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Error: Story = () => <Sample invalid />;
export const Disabled: Story = () => <Sample disabled />;
