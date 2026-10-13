import { type FormEvent, type ReactNode, useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import { useIsMobile } from '../media';
import { Modal } from './Modal';
import styles from './Overlay.module.css';
import { Sheet } from './Sheet';

export type ConfirmDialogProps = {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void; // слово совпало и нажато подтверждение (или Enter)
  title: ReactNode; // «Сбросить доступ Марии?»
  text?: ReactNode; // что произойдёт, aria-describedby
  consequences?: ReactNode[]; // список последствий (макет 07: точки danger)
  // Что ввести руками: слово («СБРОСИТЬ») или почта цели. Совпадение — точное, с учётом регистра.
  confirmWord: string;
  confirmLabel: string; // подпись кнопки подтверждения
  note?: ReactNode; // мелкий текст под кнопками («Действие попадёт в журнал…»)
  loading?: boolean; // запрос идёт: подтверждение недоступно, закрыть нельзя
  // modal — по центру, sheet — шторкой снизу; по умолчанию по ширине экрана (08: шторка).
  variant?: 'modal' | 'sheet';
};

// Подтверждение тяжёлого действия вводом слова или почты (спека §9.2, макеты 07 и 08): кнопка
// неактивна, пока введённое не совпало точно. Esc и «Отмена» — закрыть без действия; при новом
// открытии поле снова пустое.
export function ConfirmDialog({ variant, ...props }: ConfirmDialogProps) {
  const mobile = useIsMobile();
  const Shell = (variant ?? (mobile ? 'sheet' : 'modal')) === 'sheet' ? Sheet : Modal;
  return (
    <Shell
      open={props.open}
      onClose={props.onClose}
      title={props.title}
      description={props.text}
      tone="danger"
      dismissible={!props.loading}
    >
      <ConfirmBody {...props} />
    </Shell>
  );
}

function ConfirmBody({
  onClose,
  onConfirm,
  consequences,
  confirmWord,
  confirmLabel,
  note,
  loading,
}: Omit<ConfirmDialogProps, 'variant'>) {
  const t = useTranslations('ui.confirm');
  const id = useId();
  const [value, setValue] = useState('');
  const matches = value === confirmWord;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (matches && !loading) onConfirm();
  };

  return (
    <form noValidate onSubmit={submit} className={styles.confirm}>
      {consequences && consequences.length > 0 && (
        <ul className={styles.consequences}>
          {consequences.map((c, i) => (
            <li key={i}>{c}</li>
          ))}
        </ul>
      )}
      <label htmlFor={id} className={styles.prompt}>
        {t('prompt', { word: confirmWord })}
      </label>
      <input
        id={id}
        className={styles.word}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoComplete="off"
        autoCapitalize="off"
        spellCheck={false}
        disabled={loading}
      />
      <div className={styles.actions}>
        <Button variant="secondary" onClick={onClose} disabled={loading}>
          {t('cancel')}
        </Button>
        <Button type="submit" variant="danger" disabled={!matches} loading={loading}>
          {confirmLabel}
        </Button>
      </div>
      {note && <p className={styles.note}>{note}</p>}
    </form>
  );
}
