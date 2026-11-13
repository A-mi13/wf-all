import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { Switch } from './Switch';

// Переключатель (флаги функций по городам): выключен и включён, недоступен, занят, подпись только для читалки.
export default { title: 'UI/Switch', visual: true } satisfies StoryMeta;

function Pair(props: { disabled?: boolean; busy?: boolean; hideLabel?: boolean }) {
  const t = useTranslations('flags');
  const [a, setA] = useState(false);
  const [b, setB] = useState(true);
  return (
    <div className={styles.column}>
      <Switch checked={a} onChange={setA} label={t('subscriptions.title')} {...props} />
      <Switch checked={b} onChange={setB} label={t('economy.title')} {...props} />
    </div>
  );
}

export const Default: Story = () => <Pair />;
export const Disabled: Story = () => <Pair disabled />;
export const Busy: Story = () => <Pair busy />;
export const HiddenLabel: Story = () => <Pair hideLabel />;
