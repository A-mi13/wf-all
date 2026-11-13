import { useTranslations } from 'use-intl';
import { Button, CopyButton } from '../../ui';
import styles from './Auth.module.css';

// Резервные коды — один раз после выдачи (настройка 2FA, перевыпуск в «Моём доступе»): список,
// «Копировать: резервные коды» (CopyButton: буфер, при отказе — поле «вручную» рядом, тоже только в DOM),
// «Скачать .txt». Коды живут только в памяти экрана, который их показал: панель их не сохраняет и в сеть не
// отправляет (файл собирается на клиенте Blob-ом).
export function RecoveryCodesPanel({ codes }: { codes: string[] }) {
  const t = useTranslations('auth.setup');

  const download = () => {
    const text = [t('file_header'), '', ...codes, ''].join('\n');
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = t('file_name');
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return (
    <>
      <h2 className={styles.sectionTitle}>{t('codes_title')}</h2>
      <p className={styles.note}>{t('codes_text')}</p>
      {/* role="list": Safari снимает семантику списка с list-style: none */}
      <ul role="list" aria-label={t('codes_title')} className={styles.codes}>
        {codes.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className={styles.row}>
        <CopyButton value={codes.join('\n')} label={t('copy_codes')} display="text" />
        <Button variant="secondary" onClick={download}>
          {t('download')}
        </Button>
      </div>
    </>
  );
}
