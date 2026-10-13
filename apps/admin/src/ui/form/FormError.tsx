import { useContext } from 'react';
import type { ApiError } from '../../api/client';
import { useErrorText } from '../errorText';
import { FormErrorContext } from './context';
import styles from './Field.module.css';

// Общий текст ошибки: внутри Form — ошибка её последней отправки; вне формы — переданная error
// (мутация кнопки, переключатель). null/undefined — ничего.
export function FormError({ error }: { error?: ApiError | null }) {
  const fromForm = useContext(FormErrorContext);
  const text = useErrorText();
  const e = error === undefined ? fromForm : error;
  if (!e) return null;
  return (
    <p role="alert" className={styles.formError}>
      {text(e)}
    </p>
  );
}
