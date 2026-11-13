import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { CountBadge } from './CountBadge';

// Счётчик пункта меню: 0 не рисуется, больше 99 — «99+»; active — на выбранном пункте.
export default { title: 'UI/CountBadge' } satisfies StoryMeta;

function Counts({ active }: { active?: boolean }) {
  const t = useTranslations('nav.counts');
  return (
    <div className={styles.row}>
      {[0, 3, 42, 100, 1500].map((count) => (
        <CountBadge key={count} count={count} active={active} label={t('approvals', { count })} />
      ))}
    </div>
  );
}

export const Default: Story = () => <Counts />;
export const Active: Story = () => <Counts active />;
