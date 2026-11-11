import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Calendar } from './Calendar';
import { DateField } from './DateField';
import { Form } from './Form';
import { parseIso, type Ymd } from './isoDate';

// Поле даты (спека UI-фундамента §4.17): пусто (формат языка в плейсхолдере), заполнено, ошибка, недоступно;
// сетка календаря — выбранный день, недоступные после max, неделя по языку (ru — с понедельника); открытый
// календарь у поля (широкий экран — пузырь Popover, 390 — шторка).
export default { title: 'UI/Form/DateField', visual: true } satisfies StoryMeta;

function Sample({
  from = '',
  invalid,
  disabled,
  open,
}: {
  from?: string;
  invalid?: boolean;
  disabled?: boolean;
  open?: boolean;
}) {
  const t = useTranslations('audit.filters');
  const form = useForm({ defaultValues: { from, to: '' } });
  useEffect(() => {
    if (invalid) form.setError('from', { type: 'format' });
  }, [form, invalid]);
  return (
    <Form form={form} onSubmit={() => undefined} className={styles.column}>
      <DateField name="from" label={t('from')} disabled={disabled} defaultOpen={open} />
      <DateField name="to" label={t('to')} hint={t('period_hint')} disabled={disabled} />
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Filled: Story = () => <Sample from="2026-10-01" />;
export const Error: Story = () => <Sample from="31.02.2026" invalid />;
export const Disabled: Story = () => <Sample from="2026-10-01" disabled />;
export const Open: Story = () => <Sample from="2026-10-15" open />;

export const CalendarGrid: Story = () => {
  const [value, setValue] = useState<Ymd | null>(parseIso('2026-10-15'));
  return (
    <div className={styles.column}>
      <Calendar value={value} max={parseIso('2026-10-20')} onSelect={setValue} />
    </div>
  );
};
CalendarGrid.storyName = 'Calendar grid';
