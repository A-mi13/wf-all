import { render, screen } from '@testing-library/react';
import { useNow, useTranslations } from 'use-intl';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { useTheme } from '../ui/theme';
import {
  bridgeTheme,
  ladleToTheme,
  parseShowcaseLocale,
  SHOWCASE_NOW,
  themeToLadle,
} from './params';
import { ShowcaseProvider } from './Provider';
import { storyList } from './storyList';

// Витрина (спека UI-фундамента §5.2, план UF3.2): обёртка историй, мост темы Ладла и наша тема, список
// историй для снимков.

const html = document.documentElement;
afterEach(() => {
  delete html.dataset.theme;
});

// MutationObserver отдаёт записи микрозадачей — дождаться её
const flush = () => new Promise<void>((r) => setTimeout(r, 0));

describe('тема Ладла ↔ наша', () => {
  test('auto Ладла — system, light/dark — как есть, незнакомое — system', () => {
    expect(ladleToTheme('auto')).toBe('system');
    expect(ladleToTheme('light')).toBe('light');
    expect(ladleToTheme('dark')).toBe('dark');
    expect(ladleToTheme('sepia')).toBe('system');
    expect(themeToLadle('system')).toBe('auto');
    expect(themeToLadle('light')).toBe('light');
    expect(themeToLadle('dark')).toBe('dark');
  });

  test('язык витрины — из адреса: en или ru', () => {
    expect(parseShowcaseLocale('?story=a&locale=en')).toBe('en');
    expect(parseShowcaseLocale('?locale=ru')).toBe('ru');
    expect(parseShowcaseLocale('?locale=de')).toBe('ru');
    expect(parseShowcaseLocale('')).toBe('ru');
  });

  test('атрибут auto (его пишет Ладл) становится system — tokens.css его знает', async () => {
    const onChange = vi.fn();
    const stop = bridgeTheme(html, 'auto', onChange);
    html.dataset.theme = 'auto';
    await flush();
    expect(html.dataset.theme).toBe('system');
    expect(onChange).not.toHaveBeenCalled();
    stop();
  });

  test('auto уже стоит до моста — сразу system', () => {
    html.dataset.theme = 'auto';
    const stop = bridgeTheme(html, 'auto', vi.fn());
    expect(html.dataset.theme).toBe('system');
    stop();
  });

  test('история сменила тему (ThemeSwitch) — Ладл узнаёт новую', async () => {
    html.dataset.theme = 'dark';
    const onChange = vi.fn();
    const stop = bridgeTheme(html, 'dark', onChange);
    html.dataset.theme = 'light';
    await flush();
    expect(onChange).toHaveBeenCalledExactlyOnceWith('light');
    html.dataset.theme = 'system';
    await flush();
    expect(onChange).toHaveBeenLastCalledWith('auto');
    stop();
  });

  test('та же тема, что у Ладла, — без сигнала; после stop — тишина', async () => {
    const onChange = vi.fn();
    const stop = bridgeTheme(html, 'light', onChange);
    html.dataset.theme = 'light';
    await flush();
    expect(onChange).not.toHaveBeenCalled();
    stop();
    html.dataset.theme = 'dark';
    await flush();
    expect(onChange).not.toHaveBeenCalled();
  });
});

function Probe() {
  const t = useTranslations('ui.theme');
  const now = useNow();
  const { theme } = useTheme();
  return (
    <p>
      {t('label')} {now.toISOString()} {theme}
    </p>
  );
}

describe('ShowcaseProvider', () => {
  test('язык, зафиксированное «сейчас», тема и холст истории', () => {
    html.dataset.theme = 'light';
    const { container } = render(
      <ShowcaseProvider theme="light" locale="en">
        <Probe />
      </ShowcaseProvider>,
    );
    expect(screen.getByText(`Theme ${SHOWCASE_NOW.toISOString()} light`)).toBeInTheDocument();
    expect(SHOWCASE_NOW.toISOString()).toBe('2026-10-28T12:00:00.000Z');
    expect(container.querySelector('[data-showcase-root]')).not.toBeNull();
  });

  // Кнопка темы Ладла: новая тема приходит пропсом раньше, чем Ладл перепишет атрибут (его эффект — после
  // эффектов детей), — ThemeProvider не должен смонтироваться со старым атрибутом.
  test('смена темы витрины — ThemeProvider и атрибут с новой темой, хотя атрибут ещё старый', () => {
    html.dataset.theme = 'light';
    const { rerender } = render(
      <ShowcaseProvider theme="light" locale="ru">
        <Probe />
      </ShowcaseProvider>,
    );
    rerender(
      <ShowcaseProvider theme="dark" locale="ru">
        <Probe />
      </ShowcaseProvider>,
    );
    expect(screen.getByText(/ dark$/)).toBeInTheDocument();
    expect(html.dataset.theme).toBe('dark');
  });

  test('ставит data-theme и lang на документ своего корня (iframe режима ширины)', () => {
    const frame = document.createElement('iframe');
    document.body.append(frame);
    const doc = frame.contentDocument;
    if (!doc) throw new Error('нет документа iframe');
    const host = doc.body.appendChild(doc.createElement('div'));
    html.dataset.theme = 'dark';
    render(
      <ShowcaseProvider theme="dark" locale="ru">
        <Probe />
      </ShowcaseProvider>,
      { container: host },
    );
    expect(doc.documentElement.dataset.theme).toBe('dark');
    expect(doc.documentElement.lang).toBe('ru');
    frame.remove();
  });
});

describe('список историй для снимков', () => {
  const meta = {
    stories: {
      'ui--button--default': {
        name: 'Default',
        filePath: 'src/ui/Button.stories.tsx',
        namedExport: 'Default',
      },
      'ui--button--busy': {
        name: 'Busy',
        filePath: 'src/ui/Button.stories.tsx',
        namedExport: 'Busy',
      },
      'ui--shell--mobile': {
        name: 'Mobile',
        filePath: 'src/ui/shell/AppShell.stories.tsx',
        namedExport: 'Mobile',
      },
      'ui--chip--default': {
        name: 'Default',
        filePath: 'src/ui/StatusChip.stories.tsx',
        namedExport: 'Default',
      },
    },
  };
  const sources: Record<string, string> = {
    'src/ui/Button.stories.tsx': `export default { title: 'UI/Button', visual: true } satisfies StoryMeta;
export const Default: Story = () => null;
export const Busy: Story = () => null;
Busy.meta = { title: 'UI/Button', visual: false };`,
    'src/ui/shell/AppShell.stories.tsx': `export default {
  title: 'UI/Shell',
  visual: true,
  layout: true,
} satisfies StoryMeta;
export const Mobile: Story = () => null;`,
    'src/ui/StatusChip.stories.tsx': `export default { title: 'UI/StatusChip' } satisfies StoryMeta;
export const Default: Story = () => null;`,
  };
  const read = (file: string) => {
    const s = sources[file];
    if (s === undefined) throw new Error(`нет файла ${file}`);
    return s;
  };

  test('метки файла, переопределение историей, без меток — false; порядок по id', () => {
    expect(storyList(meta, read)).toEqual([
      { id: 'ui--button--busy', title: 'UI/Button', visual: false, layout: false },
      { id: 'ui--button--default', title: 'UI/Button', visual: true, layout: false },
      { id: 'ui--chip--default', title: 'UI/StatusChip', visual: false, layout: false },
      { id: 'ui--shell--mobile', title: 'UI/Shell', visual: true, layout: true },
    ]);
  });

  test('метка не литералом — понятная ошибка с файлом, а не тихое false', () => {
    const bad = {
      stories: { 'x--a': { name: 'A', filePath: 'src/ui/X.stories.tsx', namedExport: 'A' } },
    };
    const src = `const VISUAL = true;
export default { title: 'UI/X', visual: VISUAL } satisfies StoryMeta;
export const A: Story = () => null;`;
    expect(() => storyList(bad, () => src)).toThrow(/src\/ui\/X\.stories\.tsx.*visual/);
  });

  test('нет default-экспорта с title — ошибка с файлом', () => {
    const bad = {
      stories: { 'x--a': { name: 'A', filePath: 'src/ui/Y.stories.tsx', namedExport: 'A' } },
    };
    expect(() => storyList(bad, () => 'export const A = () => null;')).toThrow(/Y\.stories\.tsx/);
  });
});
