import '../src/app/styles';
import type { GlobalProvider } from '@ladle/react';
import { IntlProvider } from 'use-intl';
import { messagesFor } from '../src/i18n/messages';
import { ThemeProvider } from '../src/ui/theme';
import { ToastProvider } from '../src/ui/toast';

// Проба UF3.1: обёртка историй — стили админки (токены, шрифты), тема (Ladle ставит data-theme на <html>,
// ThemeProvider берёт атрибут при монтировании — key перемонтирует его при смене темы Ладла), язык — наш
// параметр адреса ?locale=en (Ladle сохраняет чужие параметры).
const locale = new URLSearchParams(location.search).get('locale') === 'en' ? 'en' : 'ru';

export const Provider: GlobalProvider = ({ children, globalState }) => (
  <ThemeProvider key={globalState.theme}>
    <IntlProvider locale={locale} messages={messagesFor(locale)} timeZone="UTC">
      <ToastProvider>{children}</ToastProvider>
    </IntlProvider>
  </ThemeProvider>
);
