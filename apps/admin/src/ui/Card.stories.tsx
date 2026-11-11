import { useFormatter, useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { Card } from './Card';
import { StatusChip } from './StatusChip';

// Карточка «Города и запуск»: живой город (выделен), пилот без района, длинное название.
export default { title: 'UI/Card', visual: true } satisfies StoryMeta;

function City(props: {
  name: string;
  status: 'live' | 'pilot';
  districts: number;
  selected?: boolean;
  link?: boolean;
}) {
  const t = useTranslations('geo');
  const format = useFormatter();
  return (
    <Card
      title={props.name}
      chip={
        <StatusChip tone={props.status === 'live' ? 'success' : 'warning'}>
          {t(`status.${props.status}`)}
        </StatusChip>
      }
      stats={[
        { value: format.number(450_000), label: t('cities.population') },
        { value: props.districts, label: t('cities.districts', { count: props.districts }) },
      ]}
      note={t('cities.no_region')}
      action={
        props.link
          ? {
              label: t('cities.open'),
              name: t('cities.open_label', { name: props.name }),
              href: '#city',
              onNavigate: () => undefined,
            }
          : { label: t('cities.open'), onClick: () => undefined }
      }
      selected={props.selected}
    />
  );
}

// названия городов — данные справочника, не тексты интерфейса
export const Default: Story = () => (
  <div className={styles.grid}>
    <City name="Ставрополь" status="live" districts={4} selected />
    <City name="Пятигорск" status="pilot" districts={0} />
  </div>
);

export const LongText: Story = () => (
  <div className={styles.grid}>
    <City name="Санкт-Петербург и Ленинградская агломерация" status="pilot" districts={18} />
  </div>
);

// Действие-ссылка («Открыть: <город>», G8 m11) — как на экране; узкий контейнер: ссылка во всю ширину карточки
// и не выходит за её рамку (раунд 1, I2).
export const LinkAction: Story = () => (
  <div className={styles.grid}>
    <City name="Ставрополь" status="live" districts={4} selected link />
    <div className={styles.phone}>
      <City name="Пятигорск" status="pilot" districts={0} link />
    </div>
  </div>
);
