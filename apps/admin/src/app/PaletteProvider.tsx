import { useRouter } from '@tanstack/react-router';
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { useApi } from '../api/context';
import { useAdminMutation } from '../api/mutation';
import { useApplyLocale } from '../i18n/locale';
import type { Locale } from '../i18n/messages';
import { useErrorText } from '../ui/errorText';
import { hasModalOverlay } from '../ui/overlay/stack';
import { CommandPalette } from '../ui/palette/CommandPalette';
import { type PaletteApi, PaletteContext } from '../ui/palette/context';
import { useOpenShortcutsHelp, useShortcut } from '../ui/shortcuts';
import { useDensity } from '../ui/table/prefs';
import { useTheme } from '../ui/theme/context';
import { useToast } from '../ui/toast';
import type { CommandContext } from './commands';
import { CommandShortcuts } from './CommandShortcuts';
import { useLogout } from './logout';
import { useSession } from './session';

// Палитра каркаса активной стадии (спека UI-фундамента §4.1; стоит в ActiveShell, src/app/Shell.tsx — под роутером и
// key={me.id}: другой сотрудник получает закрытую палитру с чистым состоянием). Даёт usePalette().open() кнопке
// «Искать везде», шторке «Ещё» и Ctrl/⌘+K (useShortcut, и из поля ввода); поверх модального оверлея не открывается
// (m15). Клавиши g x и «/» — CommandShortcuts.
// CommandContext — действия команд: переход роутера, тема, язык (PATCH /v1/staff/me/locale, как LocaleSwitcher;
// ошибка — тост), плотность таблиц, шпаргалка клавиш (useOpenShortcutsHelp: открыть её с переключателем
// односимвольных можно всегда — WCAG 2.1.4), выход (useLogout — с вопросом о несохранённом).
// Шпаргалка из палитры открывается после того, как палитра закрылась и вернула фокус открывателю (уборка Overlay
// идёт раньше эффекта провайдера): Modal шпаргалки запоминает открывателем его, а не снятое поле палитры.
export function PaletteProvider({ children }: { children: ReactNode }) {
  const t = useTranslations('palette');
  const session = useSession();
  const [open, setOpen] = useState(false);
  // команда «Горячие клавиши» выбрана — шпаргалка откроется эффектом того коммита, где палитра закрылась
  const helpNext = useRef(false);
  const openHelp = useOpenShortcutsHelp();
  useEffect(() => {
    if (open || !helpNext.current) return;
    helpNext.current = false;
    openHelp();
  }, [open, openHelp]);
  const router = useRouter();
  const { setTheme } = useTheme();
  const [, setDensity] = useDensity();
  const logout = useLogout();
  const api = useApi();
  const apply = useApplyLocale();
  const toast = useToast();
  const errorText = useErrorText();
  const locale = useAdminMutation(
    (next: Locale, key: string) =>
      api.PATCH('/v1/staff/me/locale', {
        params: { header: { 'Idempotency-Key': key } },
        body: { locale: next },
      }),
    {
      onSuccess: (_, next) => apply(next),
      onError: (e) => toast.error(errorText(e)),
    },
  );
  const { mutate: setLocale } = locale;
  const { mutate: signOut } = logout;

  const context = useMemo<CommandContext>(
    () => ({
      navigate: (to) => void router.navigate(to),
      setTheme,
      setLocale: (l) => setLocale(l),
      setDensity,
      openShortcuts: () => {
        helpNext.current = true;
      },
      logout: signOut,
    }),
    [router, setTheme, setLocale, setDensity, signOut],
  );
  const openPalette = useCallback(() => {
    if (hasModalOverlay()) return;
    setOpen(true);
  }, []);
  const api_ = useMemo<PaletteApi>(() => ({ open: openPalette }), [openPalette]);
  useShortcut('mod+k', openPalette, { allowInInput: true, label: t('title') });

  return (
    <PaletteContext value={api_}>
      {children}
      {session.status === 'active' && (
        <CommandShortcuts me={session.me} context={context} openPalette={openPalette} />
      )}
      <CommandPalette open={open} onClose={() => setOpen(false)} context={context} />
    </PaletteContext>
  );
}
