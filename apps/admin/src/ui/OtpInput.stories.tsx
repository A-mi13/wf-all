import { useId } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { OtpInput } from './OtpInput';

// Код второго фактора: шесть ячеек; ошибка с текстом под ячейками, занятость (запрос), закрытый ввод.
export default { title: 'UI/OtpInput', visual: true } satisfies StoryMeta;

function Sample(props: { invalid?: boolean; busy?: boolean; disabled?: boolean }) {
  const t = useTranslations('auth.mfa');
  const errorId = useId();
  return (
    <div className={styles.column}>
      <OtpInput
        label={t('code')}
        onComplete={() => undefined}
        describedBy={props.invalid ? errorId : undefined}
        {...props}
      />
      {props.invalid && <p id={errorId}>{t('invalid', { count: 2 })}</p>}
    </div>
  );
}

export const Default: Story = () => <Sample />;
export const Error: Story = () => <Sample invalid />;
export const Busy: Story = () => <Sample busy />;
export const Disabled: Story = () => <Sample disabled />;
