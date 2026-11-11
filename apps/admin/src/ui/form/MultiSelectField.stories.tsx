import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Form } from './Form';
import { MultiSelectField } from './MultiSelectField';

// Несколько значений (города модератора): закрыт — пусто с поиском, выбранное чипами, ошибка, недоступно;
// открыт — список с отмеченными (на ширине < breakpoint-mobile — шторка, снимается и на 390).
export default { title: 'UI/Form/MultiSelectField', visual: true } satisfies StoryMeta;

const CITIES = [
  { value: 'c1', label: 'Ставрополь, Ставропольский край' },
  { value: 'c2', label: 'Михайловск, Ставропольский край' },
  { value: 'c3', label: 'Пятигорск, Ставропольский край' },
  { value: 'c4', label: 'Екатеринбург, Свердловская область' },
];

function Sample({
  cities = [],
  invalid,
  disabled,
  open,
}: {
  cities?: string[];
  invalid?: boolean;
  disabled?: boolean;
  open?: boolean;
}) {
  const t = useTranslations('staff.invite');
  const form = useForm({ defaultValues: { city_ids: cities } });
  useEffect(() => {
    if (invalid) form.setError('city_ids', { type: 'min_items' });
  }, [form, invalid]);
  return (
    <Form form={form} onSubmit={() => undefined} className={styles.column}>
      <MultiSelectField
        name="city_ids"
        label={t('cities')}
        hint={t('cities_hint')}
        options={CITIES}
        searchable
        searchPlaceholder={t('cities_search')}
        disabled={disabled}
        defaultOpen={open}
      />
    </Form>
  );
}

export const Default: Story = () => <Sample />;
export const Selected: Story = () => <Sample cities={['c1', 'c3']} />;
export const Error: Story = () => <Sample invalid />;
export const Disabled: Story = () => <Sample cities={['c2']} disabled />;
export const Open: Story = () => <Sample cities={['c1', 'c3']} open />;
Open.storyName = 'Open';
