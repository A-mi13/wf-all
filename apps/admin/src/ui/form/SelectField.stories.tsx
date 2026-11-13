import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Form } from './Form';
import { SelectField } from './SelectField';

// Выпадающий список в форме (свой Select, §4.16), закрытый: не выбрано (placeholder), выбрано, ошибка,
// недоступен. Открытый список и шторка телефона — Select.stories.tsx.
export default { title: 'UI/Form/SelectField', visual: true } satisfies StoryMeta;

function Sample({
  role = '',
  invalid,
  disabled,
}: {
  role?: string;
  invalid?: boolean;
  disabled?: boolean;
}) {
  const t = useTranslations();
  const form = useForm({ defaultValues: { role } });
  useEffect(() => {
    if (invalid) form.setError('role', { type: 'required' });
  }, [form, invalid]);
  return (
    <Form form={form} onSubmit={() => undefined} className={styles.column}>
      <SelectField
        name="role"
        label={t('staff.invite.role')}
        placeholder={t('staff.invite.role_placeholder')}
        options={(['admin', 'moderator', 'support'] as const).map((r) => ({
          value: r,
          label: t(`roles.${r}`),
        }))}
        rules={{ required: true }}
        disabled={disabled}
      />
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Selected: Story = () => <Sample role="moderator" />;
export const Error: Story = () => <Sample invalid />;
export const Disabled: Story = () => <Sample role="support" disabled />;
