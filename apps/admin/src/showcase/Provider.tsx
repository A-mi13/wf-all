import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type ReactNode, useLayoutEffect, useRef, useState } from 'react';
import { IntlProvider } from 'use-intl';
import { type Locale, messagesFor } from '../i18n/messages';
import { ThemeProvider, type ThemePref } from '../ui/theme';
import { NowProvider } from '../ui/time';
import { ToastProvider } from '../ui/toast';
import { SHOWCASE_NOW } from './params';
import styles from './Provider.module.css';

// Обёртка историй витрины (спека UI-фундамента §5.2, план UF3.2): язык, тосты, кэш запросов без сети, тема и
// холст с фоном, цветом и шрифтом админки (их задают контейнеры приложения — AppShell, AuthLayout, — а не
// body). «Сейчас» (use-intl и useNow) и пояс зафиксированы: относительное время и даты на снимках не плывут. Ладл подключает её
// из .ladle/components.tsx.

// Истории src/ui без API: запрос, если компонент его сделает, не уходит в сеть и не повторяется.
const offlineClient = () =>
  new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: Infinity,
        refetchOnWindowFocus: false,
        networkMode: 'offlineFirst',
      },
      mutations: { retry: false },
    },
  });

export function ShowcaseProvider(props: { theme: ThemePref; locale: Locale; children: ReactNode }) {
  const { theme, locale, children } = props;
  const [queryClient] = useState(offlineClient);
  const root = useRef<HTMLDivElement>(null);
  // Тема, уже записанная в атрибут: ThemeProvider берёт тему из атрибута при монтировании, а новая тема Ладла
  // приходит пропсом раньше, чем Ладл перепишет атрибут (его эффект — после эффектов детей). Поэтому сначала
  // атрибут, потом монтаж ThemeProvider: пока тема витрины не записана, его нет — смена темы размонтирует
  // его и смонтирует заново уже с новым атрибутом, до отрисовки кадра (layout-эффект).
  const [applied, setApplied] = useState<ThemePref | null>(null);

  // Атрибут и язык — и на document (его читает ThemeProvider), и на документ своего корня: режим ширины
  // Ладла рисует историю в iframe, стили он туда копирует, а data-theme — нет (проба UF3.1, п. 5).
  useLayoutEffect(() => {
    const docs = new Set([document, root.current?.ownerDocument ?? document]);
    for (const doc of docs) {
      const html = doc.documentElement;
      if (html.dataset.theme !== theme) html.dataset.theme = theme;
      html.lang = locale;
    }
    // Сначала внешний мир (атрибут), потом перерисовка с ним — до кадра; это и нужно, а не каскад: без
    // второго прохода ThemeProvider смонтировался бы со старым атрибутом (тест «смена темы витрины»).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setApplied(theme);
  }, [theme, locale]);

  return (
    <QueryClientProvider client={queryClient}>
      <IntlProvider
        locale={locale}
        messages={messagesFor(locale)}
        timeZone="UTC"
        now={SHOWCASE_NOW}
      >
        <div ref={root} className={styles.canvas} data-showcase-root="">
          {applied === theme && (
            <ThemeProvider>
              <NowProvider now={SHOWCASE_NOW}>
                <ToastProvider>{children}</ToastProvider>
              </NowProvider>
            </ThemeProvider>
          )}
        </div>
      </IntlProvider>
    </QueryClientProvider>
  );
}
