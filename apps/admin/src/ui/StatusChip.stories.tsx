import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { type ChipTone, StatusChip } from './StatusChip';

// Чип статуса: пять тонов на текстах статусов из локалей.
export default { title: 'UI/StatusChip', visual: true } satisfies StoryMeta;

export const Default: Story = () => {
  const t = useTranslations();
  const chips: [ChipTone, string][] = [
    ['neutral', t('geo.status.waitlist')],
    ['accent', t('roles.owner')],
    ['success', t('geo.status.live')],
    ['warning', t('geo.status.pilot')],
    ['danger', t('staff.status.disabled')],
  ];
  return (
    <div className={styles.row}>
      {chips.map(([tone, text]) => (
        <StatusChip key={tone} tone={tone}>
          {text}
        </StatusChip>
      ))}
    </div>
  );
};

export const LongText: Story = () => {
  const t = useTranslations('staff.status');
  return (
    <div className={styles.narrow}>
      <StatusChip tone="warning">{t('expired')}</StatusChip>
    </div>
  );
};
