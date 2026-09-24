import { type Messages, withFallback } from '@wf/i18n';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

export type Locale = 'ru' | 'en';
export const defaultLocale: Locale = 'ru';

export function messagesFor(locale: Locale): Messages {
  return locale === 'en' ? withFallback(ru, en) : ru;
}
