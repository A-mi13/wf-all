import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Switch } from '../Switch';
import { MATRIX_VIRTUALIZE_FROM } from './matrix';
import { VirtualMatrix } from './VirtualMatrix';

// Матрица с виртуальными столбцами (спека UI-фундамента §4.5, флаги функций — Task 22): первый столбец закреплён,
// «Везде» неподвижен, города — окном. 2000 столбцов: в DOM — видимые ± запас (положение — --wf-virtual-*).
// Не для визуального сравнения: окно зависит от ширины и прокрутки.
export default { title: 'UI/VirtualMatrix', visual: false, layout: true } satisfies StoryMeta;

// функции и города — данные (как в фикстурах), не тексты интерфейса
const ROWS = ['Прогнозы', 'Турниры', 'Вызовы', 'Комментарии'];
// одиннадцать — как флагов в каталоге: тяжелейший случай без виртуализации (замер порога, отчёт G9-F)
const ALL_ROWS = [
  ...ROWS,
  'Подписки',
  'Экономика',
  'Лутбоксы',
  'Таблицы',
  'Метки',
  'Реклама',
  'Вратарь',
];
const cities = (n: number) => Array.from({ length: n }, (_, i) => `Город ${i + 1}`);

function Sample({ count, rows = ROWS }: { count: number; rows?: string[] }) {
  const t = useTranslations('flagsPage');
  const columns = cities(count);
  const [on, setOn] = useState<ReadonlySet<string>>(() => new Set(['Прогнозы|Город 1']));
  const flip = (k: string) =>
    setOn((prev) => {
      const next = new Set(prev);
      if (!next.delete(k)) next.add(k);
      return next;
    });
  return (
    <VirtualMatrix
      label={t('label')}
      rows={rows}
      rowKey={(r) => r}
      corner={t('corner')}
      rowHeader={(r) => r}
      fixed={[
        {
          id: 'all',
          header: t('everywhere'),
          cell: (r) => (
            <Switch
              checked={on.has(`${r}|*`)}
              onChange={() => flip(`${r}|*`)}
              label={t('everywhere_label', { flag: r })}
              hideLabel
            />
          ),
        },
      ]}
      columns={columns}
      columnKey={(c) => c}
      columnHeader={(c) => c}
      cell={(r, c) => (
        <Switch
          checked={on.has(`${r}|${c}`)}
          onChange={() => flip(`${r}|${c}`)}
          label={t('city_label', { flag: r, city: c })}
          hideLabel
        />
      )}
    />
  );
}

export const Few: Story = () => <Sample count={4} />;
Few.storyName = 'Few columns';

export const Thousands: Story = () => <Sample count={2000} />;
Thousands.storyName = 'Two thousand columns';

export const AtThreshold: Story = () => <Sample count={MATRIX_VIRTUALIZE_FROM} rows={ALL_ROWS} />;
AtThreshold.storyName = 'At virtualization threshold';
