// Локаль без роутинга: по умолчанию ru, en — только по куке (переключателя пока нет).
import { cookies } from 'next/headers';
import { getRequestConfig } from 'next-intl/server';
import { type Locale, messagesFor } from './messages';

export default getRequestConfig(async () => {
  const locale: Locale = (await cookies()).get('locale')?.value === 'en' ? 'en' : 'ru';
  return { locale, messages: messagesFor(locale) };
});
