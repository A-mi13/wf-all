/// <reference types="node" />
// Типы Node — только ради этого стража (читает исходники с диска); import.meta.glob не годится:
// vitest отдаёт CSS-модуль прокси, а не текст.
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
// src/test/** — стенд тестов, в сборку не попадает: прокладка Popover API для jsdom вставляет
// тестовый <style> (CSP тестов не касается; спека UI-фундамента §4.8 m-b, план U20). src/shots/** и
// истории — под стражем: они идут в браузер.
const sources = files.filter(
  (f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f) && !f.startsWith('test/'),
);
// Единственная запись стилей из JS — setCssPx (ui/cssVar.ts): числа положения пузыря и строк
// виртуализации (спека §4.5, стр. 577–588). Вызыватели — только ui/popover/* и ui/virtual/* (UF2b.1).
const CSS_VAR_WRITER = 'ui/cssVar.ts';
const CSS_VAR_CALLERS = /^ui\/(?:popover|virtual)\//;
const styles = files.filter((f) => f.endsWith('.css'));

test('стражу есть что читать', () => {
  expect(sources).toContain('ui/OtpInput.tsx');
  expect(sources).toContain('ui/overlay/Overlay.tsx');
  expect(sources).toContain('features/me/LocaleSwitcher.tsx');
  expect(sources).toContain('app/router.tsx');
  expect(sources).not.toContain('ui/csp.test.ts');
  expect(sources).not.toContain('test/setup.ts');
  expect(sources).toContain(CSS_VAR_WRITER);
  expect(sources).toContain('ui/popover/Popover.tsx');
  expect(sources).toContain('ui/virtual/useVirtualRows.ts');
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
    // исключение — только сам помощник и только setProperty
    const own = f === CSS_VAR_WRITER ? src.replace(/\.style\.setProperty\(/g, '') : src;
    return rules.filter((re) => re.test(own)).map((re) => `${f}: ${re}`);
  });
  expect(bad).toEqual([]);
});

test('вызыватель setCssPx: ui/popover/* и ui/virtual/* — да, остальное — нет', () => {
  expect(CSS_VAR_CALLERS.test('ui/popover/Popover.tsx')).toBe(true);
  expect(CSS_VAR_CALLERS.test('ui/virtual/useVirtualRows.ts')).toBe(true);
  expect(CSS_VAR_CALLERS.test('features/staff/StaffPage.tsx')).toBe(false);
  expect(CSS_VAR_CALLERS.test('ui/table/DataTable.tsx')).toBe(false);
  expect(CSS_VAR_CALLERS.test('features/ui/virtual/x.ts')).toBe(false);
});

test('setCssPx — пишет только --wf-* числом; вызыватели — только ui/popover/* и ui/virtual/*', () => {
  // вызыватель — любой модуль, который импортирует cssVar (статически, динамически или реэкспортом):
  // по вызову «setCssPx(» страж обходился псевдонимом импорта (ревью UF2a/I, m5)
  const importsCssVar = /(?:from|import)\s*\(?\s*['"][^'"]*\/cssVar(?:\.ts)?['"]/;
  const callers = sources.filter(
    (f) => f !== CSS_VAR_WRITER && (importsCssVar.test(read(f)) || /\bsetCssPx\(/.test(read(f))),
  );
  expect(callers.length).toBeGreaterThan(0);
  // оба разрешённых каталога действительно пользуются помощником (иначе исключение — мёртвое)
  expect(callers.some((f) => f.startsWith('ui/popover/'))).toBe(true);
  expect(callers.some((f) => f.startsWith('ui/virtual/'))).toBe(true);
  expect(callers.filter((f) => !CSS_VAR_CALLERS.test(f))).toEqual([]);
  // помощник — ровно одна запись, имя типа WfCssVar
  const writer = read(CSS_VAR_WRITER);
  expect(writer.match(/\.style\.setProperty\(/g)).toHaveLength(1);
  expect(writer).toMatch(/type WfCssVar = `--wf-\$\{string\}`/);
  expect(writer).toMatch(/px: number/);
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
