import { useTranslations } from 'use-intl';
import { useToast } from '../../ui';

// Копирование в буфер с тостом «Скопировано» или подсказкой скопировать вручную (секрет 2FA,
// резервные коды).
export function useCopy() {
  const t = useTranslations('auth.setup');
  const toast = useToast();
  return async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t('copied'));
    } catch {
      toast.error(t('copy_failed'));
    }
  };
}
