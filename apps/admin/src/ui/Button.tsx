import type { ButtonHTMLAttributes, Ref } from 'react';
import styles from './Button.module.css';
import { cx } from './cx';

// Варианты по макетам 07, 08, 13:
// - primary — главное действие (фон accent, «ПРИГЛАСИТЬ», «ПОДТВЕРДИТЬ»);
// - secondary — второстепенное (контур, «Отмена», «Потерял доступ»);
// - danger — подтверждение опасного действия (заливка danger, «Аннулировать» в диалоге);
// - danger-outline — опасное действие в списке («ЗАВЕРШИТЬ ВСЕ ДРУГИЕ СЕАНСЫ», «Отклонить»);
// - link — текстовая кнопка («Сбросить фильтры», «Выйти»).
export type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'danger-outline' | 'link';

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: 'md' | 'lg'; // lg — кнопка экрана входа (Oswald, высота control)
  block?: boolean; // во всю ширину
  loading?: boolean; // запрос идёт: кнопка недоступна, повторный клик не уходит
  ref?: Ref<HTMLButtonElement>;
};

const VARIANT: Record<ButtonVariant, string | undefined> = {
  primary: styles.primary,
  secondary: styles.secondary,
  danger: styles.danger,
  'danger-outline': styles.dangerOutline,
  link: styles.link,
};

export function Button({
  variant = 'primary',
  size = 'md',
  block,
  loading,
  disabled,
  type = 'button',
  className,
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      // type по умолчанию button: кнопка внутри формы не отправляет её случайно
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={cx(
        styles.button,
        VARIANT[variant],
        size === 'lg' && styles.lg,
        block && styles.block,
        className,
      )}
    >
      {loading && <span className={styles.spinner} aria-hidden="true" />}
      {children}
    </button>
  );
}
