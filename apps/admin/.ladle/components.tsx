import '../src/app/styles';
import './ladle.css';
import { ActionType, type GlobalProvider, ModeState, ThemeState } from '@ladle/react';
import { useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Locale } from '../src/i18n/messages';
import { ShowcaseProvider } from '../src/showcase/Provider';
import {
  bridgeTheme,
  ladleToTheme,
  type LadleTheme,
  parseShowcaseLocale,
} from '../src/showcase/params';
import { Segmented } from '../src/ui';

// Обёртка Ладла (спека UI-фундамента §5.2, план UF3.2): стили админки (токены, шрифты — тот же модуль, что у
// приложения), ShowcaseProvider, мост темы Ладла с нашим data-theme, язык — ?locale=en. Адрес для снимков:
// ?story=<id>&mode=preview&theme=light|dark[&locale=en].

const LADLE_THEME: Record<LadleTheme, ThemeState> = {
  light: ThemeState.Light,
  dark: ThemeState.Dark,
  auto: ThemeState.Auto,
};

function LocaleBar({ value, onChange }: { value: Locale; onChange: (l: Locale) => void }) {
  const t = useTranslations('locale');
  return (
    <div className="wf-ladle-bar">
      <Segmented
        label={t('label')}
        value={value}
        onChange={onChange}
        options={(['ru', 'en'] as const).map((code) => ({
          value: code,
          text: t(`short.${code}`),
          name: t('option', { code: t(`short.${code}`), name: t(code) }),
          lang: code,
        }))}
      />
    </div>
  );
}

export const Provider: GlobalProvider = ({ children, globalState, dispatch }) => {
  const [locale, setLocale] = useState(() => parseShowcaseLocale(location.search));

  useEffect(
    () =>
      bridgeTheme(document.documentElement, globalState.theme, (t) =>
        dispatch({ type: ActionType.UpdateTheme, value: LADLE_THEME[t] }),
      ),
    [globalState.theme, dispatch],
  );

  // язык — в адрес (F5 и пересланная ссылка его сохраняют; Ладл чужие параметры не трогает)
  const changeLocale = (l: Locale) => {
    const url = new URL(location.href);
    if (l === 'ru') url.searchParams.delete('locale');
    else url.searchParams.set('locale', l);
    history.replaceState(history.state, '', url);
    setLocale(l);
  };

  return (
    <ShowcaseProvider theme={ladleToTheme(globalState.theme)} locale={locale}>
      {/* переключатель языка — только в полном режиме: снимки (mode=preview) его не видят */}
      {globalState.mode !== ModeState.Preview && (
        <LocaleBar value={locale} onChange={changeLocale} />
      )}
      {children}
    </ShowcaseProvider>
  );
};
