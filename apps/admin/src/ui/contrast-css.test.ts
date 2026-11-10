/// <reference types="node" />
// Типы Node — только ради этого стража (читает стили с диска как текст, как csp.test.ts и layout-css.test.ts).
//
// Страж контраста по CSS Modules (спека UI-фундамента §2.6 (2), (3); план UF1b.3). Пары «цвет × фон»
// собираются из самих стилей, а не из ручного списка:
// - правило с background: var(--wf-color-X) и color / stroke / fill: var(--wf-color-Y) даёт пару (Y, X);
//   рамка (border*) даёт пару только для border-control — рамки поля, у которой есть норма 3:1 к своему
//   фону (1.4.11). Декоративные рамки (border, border-strong, slot-*, chip-*) нормы не имеют (§2.6), а рамки
//   выбранного состояния (U8: accent-ink у Switch, selected-border у сегмента) меряют к внешней поверхности
//   (в тёмной теме по замыслу сливаются с заливкой или декоративны) — свой тест ниже;
// - фон и цвет в разных правилах одного класса (.x и .x:hover, .x[aria-pressed='true'], .a .x и .x)
//   сводятся по базовому классу: недостающее свойство берётся у правила базового селектора того же файла;
// - для каждой пары двух токенов нужна строка в tokens.contrast (её норму проверяет страж токенов в обеих
//   темах); color-mix(in srgb, …) вычисляется (contrast.mjs) и сверяется с нормой прямо здесь — смесь не
//   пропускается молча;
// - цвет без своего фона (на фоне родителя): текст (color) и значок (stroke/fill) — ручной список
//   поверхностей SURFACES: для такого токена нужны строки contrast со всеми ними с нормой употребления
//   (text-subtle × chip-bg запрещена, §2.6). Исключения графики — GRAPHIC_OWN_SURFACE: цвета, которые по
//   замыслу рисуются цветом поверхности или на своей плашке (вырез, вырез на акценте, QR).
// Ограничения (словами, как велит план): вложенность не разбирается — текст в дочернем классе на фоне
// родительского класса виден стражу только как «текст на поверхности» (SURFACES); @media сводятся плоско;
// системные цвета forced-colors (CanvasText, Highlight) и currentcolor/inherit пар не дают.
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tokens } from '@wf/tokens';
import { colorOf, contrastRatio, mixSrgb, relativeLuminance } from '@wf/tokens/contrast';
import { describe, expect, test } from 'vitest';

type Theme = 'dark' | 'light';
const THEMES: Theme[] = ['dark', 'light'];

// Поверхности, на которых лежит текст без своего фона (§2.6, ручной список).
const SURFACES = ['bg', 'bg-elevated', 'surface-sunken', 'chip-bg'] as const;
// Недоступное (disabled) — нормы нет (§2.6, 1.4.3: неактивные компоненты).
const NO_NORM = new Set(['text-disabled', 'text-ghost']);
// Графика без своего фона, у которой нет нормы к поверхности: вырез иконки — цвет самой поверхности
// (bg-elevated, U25), вырез и галочка на акцентной заливке — text-inverse, модули и поле QR — своя плашка.
const GRAPHIC_OWN_SURFACE = new Set(['bg-elevated', 'text-inverse', 'qr-ink', 'qr-paper']);
// Рамки с нормой к своему фону.
const NORMED_BORDERS = new Set(['border-control']);

const dir = join(import.meta.dirname, '..');
const styleFiles = (readdirSync(dir, { recursive: true }) as string[])
  .map((f) => f.replaceAll('\\', '/'))
  .filter((f) => f.endsWith('.module.css'));
const read = (f: string) => readFileSync(join(dir, f), 'utf8');

// ——— разбор CSS ———

type Rule = { file: string; selector: string; decls: Map<string, string> };

// Разделить по запятой верхнего уровня (не внутри скобок).
function splitTop(s: string, sep = ','): string[] {
  const out: string[] = [];
  let depth = 0;
  let cur = '';
  for (const ch of s) {
    if (ch === '(' || ch === '[') depth++;
    if (ch === ')' || ch === ']') depth--;
    if (ch === sep && depth === 0) {
      out.push(cur);
      cur = '';
    } else cur += ch;
  }
  out.push(cur);
  return out.map((x) => x.trim()).filter(Boolean);
}

export function parseRules(file: string, css: string): Rule[] {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, '');
  return [...text.matchAll(/([^{}]+)\{([^{}]*)\}/g)].flatMap((m) => {
    const decls = new Map<string, string>();
    for (const d of splitTop(m[2] ?? '', ';')) {
      const at = d.indexOf(':');
      if (at > 0) decls.set(d.slice(0, at).trim().toLowerCase(), d.slice(at + 1).trim());
    }
    return splitTop((m[1] ?? '').trim()).map((selector) => ({ file, selector, decls }));
  });
}

// Последнее составное звено селектора и всё до него.
function splitLast(sel: string): [string, string] {
  let depth = 0;
  for (let i = sel.length - 1; i >= 0; i--) {
    const ch = sel[i];
    if (ch === ')' || ch === ']') depth++;
    if (ch === '(' || ch === '[') depth--;
    if (depth === 0 && (ch === ' ' || ch === '>' || ch === '+' || ch === '~'))
      return [sel.slice(0, i + 1), sel.slice(i + 1)];
  }
  return ['', sel];
}

// Базовый класс звена: первый простой селектор без состояний (.segment[aria-pressed] → .segment).
const baseOf = (compound: string) => /^(\.[\w-]+|[a-z][\w-]*|\*)/i.exec(compound)?.[1] ?? compound;

function candidates(sel: string): string[] {
  const [prefix, last] = splitLast(sel);
  const base = baseOf(last);
  return [...new Set([sel, `${prefix}${base}`, last, base])];
}

// ——— цвета ———

type Color =
  | { kind: 'token'; name: string }
  | { kind: 'mix'; expr: string }
  | { kind: 'none' }
  | { kind: 'other'; expr: string };

const VAR = /^var\(--wf-color-([\w-]+)\)$/;

// Значение свойства цвета → токен, смесь или «нет цвета». В шорткатах (background, border) ищется
// единственный цвет-токен или смесь.
export function colorValue(value: string): Color {
  const v = value.trim();
  const mix = /color-mix\(.*\)/.exec(v);
  if (mix) return { kind: 'mix', expr: mix[0] };
  const vars = [...v.matchAll(/var\(--wf-color-([\w-]+)\)/g)];
  if (vars.length === 1) return { kind: 'token', name: vars[0]![1]! };
  if (vars.length > 1) return { kind: 'other', expr: v };
  return { kind: 'none' };
}

// Цвет выражения в теме: токен или color-mix(in srgb, A p%, B). Иное — ошибка: страж не пропускает
// то, чего не понимает.
export function resolveColor(expr: string, theme: Theme): string {
  const e = expr.trim();
  const token = VAR.exec(e);
  if (token) return colorOf(tokens, token[1]!, theme);
  const mix = /^color-mix\(\s*in srgb\s*,(.*)\)$/s.exec(e);
  if (mix) {
    const [a, b] = splitTop(mix[1]!);
    const pa = /^(.*?)\s+([\d.]+)%$/.exec(a ?? '');
    const pb = /^(.*?)\s+([\d.]+)%$/.exec(b ?? '');
    if (!a || !b || (pa && pb)) throw new Error(`смесь не разобрана: ${e}`);
    const ca = pa ? pa[1]! : a;
    const cb = pb ? pb[1]! : b;
    const p = pa ? Number(pa[2]) / 100 : pb ? 1 - Number(pb[2]) / 100 : 0.5;
    return mixSrgb(resolveColor(ca, theme), resolveColor(cb, theme), p);
  }
  throw new Error(`цвет не разобран стражем: ${e}`);
}

// Норма строки contrast по паре «текст|фон». Строка с нормой одной темы ({ light: 3 }, selected-border)
// для употребления в обеих темах не годится — 0.
const contrastNorm = new Map<string, number>(
  tokens.contrast.map(([fg, bg, norm]) => [`${fg}|${bg}`, typeof norm === 'number' ? norm : 0]),
);

// Норма употребления: текст — 4.5 (1.4.3), значок, линия, рамка поля — 3 (1.4.11). color значка
// (stroke: currentcolor) — по имени класса со словом icon; text-faint («только значки», §2.6) текстом
// не проходит: его строки — с нормой 3.
const normOfUse = (prop: string, selector: string) =>
  prop === 'color' && !/icon/i.test(splitLast(selector)[1]) ? 4.5 : 3;

// ——— сбор пар ———

type Issue = string;

export function analyze(rules: Rule[]): {
  missing: Issue[];
  low: Issue[];
  parentText: Map<string, { norm: number; at: string }>;
  chipSubtle: Issue[];
  pairs: Set<string>;
} {
  const bySelector = new Map<string, Map<string, string>>();
  for (const r of rules) {
    const key = `${r.file}|${r.selector}`;
    const merged = bySelector.get(key) ?? new Map<string, string>();
    for (const [k, v] of r.decls) merged.set(k, v);
    bySelector.set(key, merged);
  }
  const lookup = (r: Rule, pick: (d: Map<string, string>) => string | undefined) => {
    for (const s of candidates(r.selector)) {
      const d = bySelector.get(`${r.file}|${s}`);
      const v = d && pick(d);
      if (v !== undefined) return v;
    }
    return undefined;
  };
  const bgOf = (d: Map<string, string>) => d.get('background-color') ?? d.get('background');
  const borderOf = (d: Map<string, string>) => {
    for (const [k, v] of d)
      if (/^border(-(top|bottom|left|right|block|inline)(-(start|end))?)?(-color)?$/.test(k))
        if (colorValue(v).kind !== 'none') return v;
    return undefined;
  };

  const missing: Issue[] = [];
  const low: Issue[] = [];
  const chipSubtle: Issue[] = [];
  const pairs = new Set<string>();
  const parentText = new Map<string, { norm: number; at: string }>();
  const where = (r: Rule) => `${r.file}: ${r.selector}`;
  const onParent = (name: string, norm: number, r: Rule) => {
    const prev = parentText.get(name);
    if (!prev || prev.norm < norm) parentText.set(name, { norm, at: where(r) });
  };

  const check = (r: Rule, prop: string, fg: Color, bg: Color, norm: number) => {
    if (fg.kind === 'none' || bg.kind === 'none') return;
    if (fg.kind === 'other' || bg.kind === 'other') {
      missing.push(`${where(r)} — ${prop}: цвет не разобран`);
      return;
    }
    if (fg.kind === 'token' && bg.kind === 'token') {
      if (NO_NORM.has(fg.name)) return;
      pairs.add(`${fg.name}|${bg.name}`);
      const rowNorm = contrastNorm.get(`${fg.name}|${bg.name}`);
      if (rowNorm === undefined || rowNorm < norm)
        missing.push(`${fg.name} × ${bg.name} (${prop} ≥ ${norm}) — ${where(r)}`);
      return;
    }
    const fe = fg.kind === 'token' ? `var(--wf-color-${fg.name})` : fg.expr;
    const be = bg.kind === 'token' ? `var(--wf-color-${bg.name})` : bg.expr;
    for (const t of THEMES) {
      const ratio = contrastRatio(resolveColor(fe, t), resolveColor(be, t));
      if (ratio < norm)
        low.push(`${where(r)} — ${prop} ${fe} на ${be} (${t}): ${ratio.toFixed(2)} < ${norm}`);
    }
  };

  for (const r of rules) {
    if (r.selector.includes('::placeholder')) continue; // плейсхолдер — свой страж ниже
    const own = ['color', 'stroke', 'fill', 'background', 'background-color'].some((p) =>
      r.decls.has(p),
    );
    if (!own && borderOf(r.decls) === undefined) continue;
    const bgRaw = lookup(r, bgOf);
    const bg = bgRaw === undefined ? ({ kind: 'none' } as Color) : colorValue(bgRaw);

    const text = lookup(r, (d) => d.get('color'));
    if (text !== undefined) {
      const fg = colorValue(text);
      if (bg.kind === 'none') {
        const norm = normOfUse('color', r.selector);
        if (fg.kind === 'token' && !NO_NORM.has(fg.name)) onParent(fg.name, norm, r);
        else if (fg.kind === 'mix') {
          for (const s of SURFACES) check(r, 'color', fg, { kind: 'token', name: s }, norm);
        }
      } else check(r, 'color', fg, bg, normOfUse('color', r.selector));
      if (fg.kind === 'token' && fg.name === 'text-subtle' && bg.kind === 'token')
        if (bg.name === 'chip-bg') chipSubtle.push(where(r));
    }
    for (const p of ['stroke', 'fill']) {
      const v = lookup(r, (d) => d.get(p));
      if (v === undefined) continue;
      const fg = colorValue(v);
      if (bg.kind !== 'none') check(r, p, fg, bg, 3);
      else if (fg.kind === 'token' && !NO_NORM.has(fg.name) && !GRAPHIC_OWN_SURFACE.has(fg.name))
        onParent(fg.name, 3, r);
      else if (fg.kind === 'mix')
        for (const sf of SURFACES) check(r, p, fg, { kind: 'token', name: sf }, 3);
    }
    const border = lookup(r, borderOf);
    if (border !== undefined) {
      const c = colorValue(border);
      if (c.kind === 'token' && NORMED_BORDERS.has(c.name)) check(r, 'border', c, bg, 3);
    }
  }
  return { missing, low, parentText, chipSubtle, pairs };
}

const rules = styleFiles.flatMap((f) => parseRules(f, read(f)));
const result = analyze(rules);

// ——— тесты ———

test('стражу есть что читать', () => {
  expect(styleFiles).toContain('ui/Button.module.css');
  expect(styleFiles).toContain('ui/StatusChip.module.css');
  expect(styleFiles).toContain('ui/form/Field.module.css');
  expect(styleFiles).toContain('ui/table/DataTable.module.css');
  expect(styleFiles).toContain('ui/icon/Icon.module.css');
  expect(styleFiles).toContain('features/auth/Auth.module.css');
  // пары действительно собираются из CSS (по одной на известные места)
  expect(result.pairs).toContain('text-muted|chip-bg'); // StatusChip .neutral
  expect(result.pairs).toContain('accent-ink|pitch'); // Segmented нажатый
  expect(result.pairs).toContain('text-inverse|accent-hover'); // Button .primary:hover
  expect(result.pairs).toContain('border-control|surface-sunken'); // Field .control
  expect(result.parentText.has('text-subtle')).toBe(true);
});

test('пары из CSS Modules есть в contrast', () => {
  expect(result.missing).toEqual([]);
  expect(result.low).toEqual([]);
});

test('цвет без своего фона (текст, значок) — строки contrast со всеми поверхностями', () => {
  const bad = [...result.parentText.entries()].flatMap(([name, { norm, at }]) =>
    SURFACES.filter((s) => !(name === 'text-subtle' && s === 'chip-bg'))
      .filter((s) => (contrastNorm.get(`${name}|${s}`) ?? 0) < norm)
      .map((s) => `${name} × ${s} (≥ ${norm}) — ${at}`),
  );
  expect(bad).toEqual([]);
});

test('плейсхолдеры — text-subtle', () => {
  const placeholders = rules.filter((r) => r.selector.includes('::placeholder'));
  expect(placeholders.length).toBeGreaterThanOrEqual(2);
  const bad = placeholders
    .filter((r) => r.decls.get('color') !== 'var(--wf-color-text-subtle)')
    .map((r) => `${r.file}: ${r.selector} → ${r.decls.get('color')}`);
  expect(bad).toEqual([]);
});

test('на chip-bg нет text-subtle', () => {
  expect(result.chipSubtle).toEqual([]);
});

// §2.3: accent — только заливка; текст, линия, курсор, иконка — accent-ink, кольцо фокуса — focus.
test('accent и accent-hover — только фоном; кольцо фокуса — focus', () => {
  const bad = rules.flatMap((r) =>
    [...r.decls]
      .filter(([p, v]) => {
        const accent = /var\(--wf-color-accent(-hover)?\)/.test(v);
        if (accent && p !== 'background' && p !== 'background-color') return true;
        return (
          p.startsWith('outline') && /var\(--wf-color-/.test(v) && !v.includes('--wf-color-focus')
        );
      })
      .map(([p, v]) => `${r.file}: ${r.selector} { ${p}: ${v} }`),
  );
  expect([...new Set(bad)]).toEqual([]);
});

// Тени и затемнение — токены shadow-* и scrim (§2.3): смесь с прозрачным не вычисляется в цвет и
// в светлой теме даёт светлую тень на светлом.
test('тени и затемнение — токены, без color-mix с прозрачным', () => {
  const bad = rules.flatMap((r) =>
    [...r.decls]
      .filter(([p, v]) => /color-mix\(/.test(v) && (/transparent/.test(v) || p === 'box-shadow'))
      .map(([p]) => `${r.file}: ${r.selector} { ${p} }`),
  );
  expect(bad).toEqual([]);
  const shadow = (f: string, s: string) =>
    rules.find((r) => r.file === f && r.selector === s)?.decls.get('box-shadow');
  expect(shadow('ui/overlay/Overlay.module.css', '.panel')).toBe('var(--wf-shadow-overlay)');
  expect(shadow('ui/toast/Toast.module.css', '.toast')).toBe('var(--wf-shadow-toast)');
});

// §2.3, §2.6: граница поля, флажка, переключателя — border-control (≥ 3:1, WCAG 1.4.11); прежняя border
// давала ~1.4:1. Список полей — явный: декоративные рамки (карточки, списки) остаются border.
test('рамки полей — border-control', () => {
  const fields: [string, string][] = [
    ['ui/form/Field.module.css', '.control'],
    ['ui/form/Field.module.css', '.checkInput'],
    ['ui/OtpInput.module.css', '.cell'],
    ['ui/overlay/Overlay.module.css', '.word'],
    ['ui/table/DataTable.module.css', '.search'],
    ['ui/Switch.module.css', '.track'],
  ];
  const bad = fields.filter(([f, s]) => {
    const border = rules.find((r) => r.file === f && r.selector === s)?.decls.get('border');
    return !border?.includes('var(--wf-color-border-control)');
  });
  expect(bad).toEqual([]);
});

// U8 (+ решение пользователя, раунд 1 UF1b/I): включённый Switch — рамка accent-ink (в тёмной сливается с
// заливкой accent); выбранный сегмент (заливка pitch) — selected-border: в тёмной прежняя рамка артборда 07,
// в светлой accent-ink (3:1 к соседнему фону — строка contrast с нормой только светлой).
test('рамка выбранного сегмента — selected-border, включённого переключателя — accent-ink', () => {
  const decl = (f: string, s: string, p: string) =>
    rules.find((r) => r.file === f && r.selector === s)?.decls.get(p);
  expect(decl('ui/Segmented.module.css', ".segment[aria-pressed='true']", 'border-color')).toBe(
    'var(--wf-color-selected-border)',
  );
  expect(decl('ui/Switch.module.css', '.on', 'border-color')).toBe('var(--wf-color-accent-ink)');
});

// Ревью UF1a ⚠️: деталь акцента иконки — тень, а не блик: в обеих темах темнее акцента иконки
// (как рисует CSS: токен или смесь вычисляются).
test('иконки: accent-detail темнее акцента в обеих темах', () => {
  const stroke = (s: string) => {
    const v = rules.find((r) => r.file === 'ui/icon/Icon.module.css' && r.selector === s)?.decls;
    expect(v, s).toBeDefined();
    return v!.get('stroke') ?? '';
  };
  const detail = stroke('.accentDetail.stroke');
  const accent = stroke('.accent.stroke');
  expect(accent).toBe('var(--wf-color-accent-ink)'); // U7
  for (const t of THEMES)
    expect(
      relativeLuminance(resolveColor(detail, t)),
      `${t}: ${detail} не темнее ${accent}`,
    ).toBeLessThan(relativeLuminance(resolveColor(accent, t)));
});

// Сам страж: color-mix вычисляется, а не пропускается; непонятное — ошибка.
describe('разбор стража', () => {
  const run = (css: string) => analyze(parseRules('x.module.css', css));

  test('смесь в паре вычисляется и сверяется с нормой', () => {
    const bad = run(
      '.a { background: var(--wf-color-bg-elevated); color: color-mix(in srgb, var(--wf-color-text-ghost) 60%, var(--wf-color-bg-elevated)); }',
    );
    expect(bad.low).toHaveLength(2); // обе темы ниже 4.5
    const ok = run(
      '.a { background: var(--wf-color-bg); color: color-mix(in srgb, var(--wf-color-text) 90%, var(--wf-color-bg)); }',
    );
    expect(ok.low).toEqual([]);
    expect(ok.missing).toEqual([]);
  });

  test('смесь с bg в светлой теме светлее акцента', () => {
    const mix = 'color-mix(in srgb, var(--wf-color-accent) 70%, var(--wf-color-bg))';
    expect(resolveColor(mix, 'dark')).toBe('#8EAE3A');
    expect(relativeLuminance(resolveColor(mix, 'light'))).toBeGreaterThan(
      relativeLuminance(resolveColor('var(--wf-color-accent)', 'light')),
    );
    expect(() => resolveColor('color-mix(in oklch, red, blue)', 'dark')).toThrow();
    expect(() => resolveColor('var(--wf-color-nope)', 'dark')).toThrow();
  });

  test('фон и цвет сводятся по базовому классу', () => {
    const r = run(
      ".x { color: var(--wf-color-text-faint); } .x[aria-pressed='true'] { background: var(--wf-color-pitch); } .p .x:hover { background: var(--wf-color-chip-bg); color: var(--wf-color-text-subtle); }",
    );
    expect(r.missing).toEqual([
      "text-faint × pitch (color ≥ 4.5) — x.module.css: .x[aria-pressed='true']",
      'text-subtle × chip-bg (color ≥ 4.5) — x.module.css: .p .x:hover',
    ]);
    expect(r.chipSubtle).toEqual(['x.module.css: .p .x:hover']);
  });

  // text-faint × chip-bg есть в contrast с нормой 3 (значки): текстом (4.5) пара не проходит (I9).
  test('норма строки — не ниже нормы употребления: текст 4.5, значок 3', () => {
    const text = run(
      '.chip { background: var(--wf-color-chip-bg); color: var(--wf-color-text-faint); }',
    );
    expect(text.missing).toEqual(['text-faint × chip-bg (color ≥ 4.5) — x.module.css: .chip']);
    const icon = run(
      '.chip { background: var(--wf-color-chip-bg); } .chip .chipIcon { color: var(--wf-color-text-faint); }',
    );
    expect(icon.missing).toEqual([]);
    expect(icon.parentText.get('text-faint')?.norm).toBe(3); // значок на фоне родителя — норма 3
    const ownIcon = run(
      '.chipIcon { background: var(--wf-color-chip-bg); color: var(--wf-color-text-faint); }',
    );
    expect(ownIcon.missing).toEqual([]); // значок со своим фоном — строка с нормой 3 годится
    const asText = run('.chipLabel { color: var(--wf-color-text-faint); }');
    expect(asText.parentText.get('text-faint')?.norm).toBe(4.5);
    const stroke = run(
      '.a { background: var(--wf-color-chip-bg); stroke: var(--wf-color-text-faint); }',
    );
    expect(stroke.missing).toEqual([]);
  });

  // Ревью UF1b/I, m3: обводка без своего фона (слой иконки) — на поверхности родителя, норма 3.
  test('stroke/fill без своего фона — в списке «на поверхности», вырез и QR — нет', () => {
    const r = run(
      '.detail.stroke { stroke: var(--wf-color-border); } .knockout.fill { fill: var(--wf-color-bg-elevated); } .dark { fill: var(--wf-color-qr-ink); }',
    );
    expect(r.parentText.get('border')).toEqual({
      norm: 3,
      at: 'x.module.css: .detail.stroke',
    });
    expect(r.parentText.has('bg-elevated')).toBe(false);
    expect(r.parentText.has('qr-ink')).toBe(false);
  });
});
