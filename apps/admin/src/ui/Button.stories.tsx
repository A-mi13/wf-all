import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { Button, type ButtonVariant } from './Button';

// Кнопка (спека сотрудников §9.2): пять вариантов, размер входа, занятость и недоступность.
export default { title: 'UI/Button', visual: true } satisfies StoryMeta;

const VARIANTS: ButtonVariant[] = ['primary', 'secondary', 'danger', 'danger-outline', 'link'];

function Variants(props: { disabled?: boolean; loading?: boolean }) {
  const t = useTranslations();
  return (
    <div className={styles.row}>
      {VARIANTS.map((v) => (
        <Button key={v} variant={v} {...props}>
          {t('staff.invite.open')}
        </Button>
      ))}
    </div>
  );
}

export const Default: Story = () => <Variants />;
export const Disabled: Story = () => <Variants disabled />;
export const Busy: Story = () => <Variants loading />;

export const Large: Story = () => {
  const t = useTranslations('auth.login');
  return (
    <div className={styles.column}>
      <Button size="lg" block>
        {t('submit')}
      </Button>
    </div>
  );
};

export const LongText: Story = () => {
  const t = useTranslations('auth.login');
  return (
    <div className={styles.column}>
      <Button variant="secondary">{t('lead')}</Button>
    </div>
  );
};
