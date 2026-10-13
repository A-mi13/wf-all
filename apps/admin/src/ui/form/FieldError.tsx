import { type FieldError as RhfFieldError, get, useFormContext } from 'react-hook-form';
import { useFieldErrorText } from './field';
import styles from './Field.module.css';

// Текст ошибки поля формы name. Поля набора ставят его сами; своё поле (Controller с OtpInput и
// т. п.) ставит <FieldError name id /> и связывает id через aria-describedby.
export function FieldError({ name, id }: { name: string; id?: string }) {
  const { formState } = useFormContext();
  const text = useFieldErrorText();
  const error = get(formState.errors, name) as RhfFieldError | undefined;
  if (!error) return null;
  return (
    <p id={id} className={styles.error}>
      {text(error)}
    </p>
  );
}
