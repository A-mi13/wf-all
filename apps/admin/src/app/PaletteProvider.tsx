import { useRouter } from '@tanstack/react-router';
import { type ReactNode, useCallback, useMemo, useState } from 'react';
import { useApi } from '../api/context';
import { useAdminMutation } from '../api/mutation';
import { useApplyLocale } from '../i18n/locale';
import type { Locale } from '../i18n/messages';
import { useErrorText } from '../ui/errorText';
import { hasModalOverlay } from '../ui/overlay/stack';
import { CommandPalette } from '../ui/palette/CommandPalette';
import { type PaletteApi, PaletteContext } from '../ui/palette/context';
import { useDensity } from '../ui/table/prefs';
import { useTheme } from '../ui/theme/context';
import { useToast } from '../ui/toast';
import type { CommandContext } from './commands';
import { useLogout } from './logout';

// Палитра каркаса активной стадии (спека UI-фундамента §4.1; стоит в ActiveShell, src/app/Shell.tsx — под роутером и
// key={me.id}: другой сотрудник получает закрытую палитру с чистым состоянием). Даёт usePalette().open() кнопке
// «Искать везде», шторке «Ещё» и Ctrl/⌘+K (волна 2); поверх модального оверлея не открывается (m15).
// CommandContext — действия команд: переход роутера, тема, язык (PATCH /v1/staff/me/locale, как LocaleSwitcher;
// ошибка — тост), плотность таблиц, шпаргалка клавиш (openShortcuts — волна 2 подключит ShortcutsHelp; до неё
// команда ничего не делает), выход (useLogout — с вопросом о несохранённом).
export function PaletteProvider({
  children,
  openShortcuts,
}: {
  children: ReactNode;
  openShortcuts?: () => void;
}) {
  const [open, setOpen] = useState(false);
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
      openShortcuts: openShortcuts ?? (() => {}),
      logout: signOut,
    }),
    [router, setTheme, setLocale, setDensity, openShortcuts, signOut],
  );
  const openPalette = useCallback(() => {
    if (hasModalOverlay()) return;
    setOpen(true);
  }, []);
  const api_ = useMemo<PaletteApi>(() => ({ open: openPalette }), [openPalette]);

  return (
    <PaletteContext value={api_}>
      {children}
      <CommandPalette open={open} onClose={() => setOpen(false)} context={context} />
    </PaletteContext>
  );
}
