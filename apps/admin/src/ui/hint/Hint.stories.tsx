import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import stories from '../../showcase/Stories.module.css';
import { Button } from '../Button';
import { type Column, DataTable } from '../table';
import { GLOSSARY_KEYS, type GlossaryKey } from './glossary';
import { TermHint } from './TermHint';
import { Tooltip } from './Tooltip';

// Подсказка и термин (спека UI-фундамента §4.8): подсказка у кнопки (открыта); две подсказки разом —
// у первой и у средней строки DataTable (страж положения N3 в визуальном наборе, подсказки открыты
// пропом open); термин с «i» в тексте.
export default { title: 'UI/Hint', visual: true } satisfies StoryMeta;

export const TooltipOpen: Story = () => {
  const t = useTranslations();
  return (
    <Tooltip content={t('glossary.recovery_codes')} open>
      <Button variant="secondary">{t('nav.me')}</Button>
    </Tooltip>
  );
};
TooltipOpen.storyName = 'Tooltip open';

type Row = { id: string; key: GlossaryKey };
const ROWS: Row[] = GLOSSARY_KEYS.slice(0, 5).map((key, i) => ({ id: String(i), key }));

export const TwoTooltipsInTable: Story = () => {
  const t = useTranslations();
  const columns: Column<Row>[] = [
    {
      id: 'term',
      header: t('staff.matrix.permission'),
      cell: (r) => (
        <Tooltip content={t(`glossary.${r.key}`)} open={r.id === '0' || r.id === '2'}>
          <Button variant="link">{r.key}</Button>
        </Tooltip>
      ),
    },
  ];
  return (
    <DataTable
      label={t('staff.matrix.label')}
      columns={columns}
      rowKey={(r) => r.id}
      rows={ROWS}
      summary={false}
      onRetry={() => {}}
    />
  );
};
TwoTooltipsInTable.storyName = 'Two tooltips first and middle row';

export const Term: Story = () => {
  const t = useTranslations();
  return (
    <p className={stories.column}>
      <TermHint term="dual_approval">{t('staff.two_hands.title')}</TermHint>
    </p>
  );
};
Term.storyName = 'Term with info';
