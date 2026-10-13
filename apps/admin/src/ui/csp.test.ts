import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from 'vitest';

// CSP сборки админки (admin-api, Task 9b): style-src 'self' без 'unsafe-inline'. Код админки
// (весь src, кроме тестов) не вставляет <style> и HTML строкой, не пишет атрибут style и
// element.style — стили только CSS Modules, цвета только токены --wf-* (правило frontend.md).
// Страж читает исходники как текст.
const dir = join(import.meta.dirname, '..');
const files = (readdirSync(dir, { recursive: true }) as string[]).map((f) =>
  f.replaceAll('\\', '/'),
);
const read = (f: string) => readFileSync(join(dir, f), 'utf8');
const sources = files.filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f));
const styles = files.filter((f) => f.endsWith('.css'));

test('стражу есть что читать', () => {
  expect(sources).toContain('ui/OtpInput.tsx');
  expect(sources).toContain('ui/overlay/Overlay.tsx');
  expect(sources).toContain('features/me/LocaleSwitcher.tsx');
  expect(sources).toContain('app/router.tsx');
  expect(sources).not.toContain('ui/csp.test.ts');
  expect(styles).toContain('ui/Button.module.css');
  expect(styles).toContain('ui/toast/Toast.module.css');
});

test('компоненты не пишут inline-стили и не вставляют <style>', () => {
  const rules = [
    /\bstyle\s*=\s*\{/,
    /<style[\s>]/,
    /dangerouslySetInnerHTML/,
    /\.style\.\w+\s*=/,
    /\.style\.(setProperty|cssText)/,
    /setAttribute\(\s*['"]style/,
    /CSSStyleSheet|insertRule|adoptedStyleSheets/,
  ];
  const bad = sources.flatMap((f) => {
    const src = read(f);
    return rules.filter((re) => re.test(src)).map((re) => `${f}: ${re}`);
  });
  expect(bad).toEqual([]);
});

test('цвета в стилях — только токены --wf-*', () => {
  const rules = [
    /#[0-9a-f]{3,8}\b/i,
    /\brgba?\(/i,
    /\bhsla?\(/i,
    /(?<![\w-])(white|black)(?![\w-])/i,
  ];
  const bad = styles.flatMap((f) => {
    const css = read(f);
    return rules.filter((re) => re.test(css)).map((re) => `${f}: ${re}`);
  });
  expect(bad).toEqual([]);
});
