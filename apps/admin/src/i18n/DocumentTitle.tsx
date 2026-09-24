import { useEffect } from 'react';
import { useTranslations } from 'use-intl';

// Заголовок вкладки — из messages, а не литералом в index.html (строки UI только в локалях).
export function DocumentTitle() {
  const t = useTranslations('app');
  const title = t('title');
  useEffect(() => {
    document.title = title;
  }, [title]);
  return null;
}
