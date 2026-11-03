// Выгрузка иконок из артборда 14 (design/screens/14-Иконки.dc.html) в packages/icons/icons.json
// (спека UI-фундамента §4.9, план UF1a.2, решения U5, U25):
//   node design/tools/extract-icons.mjs
// Результат коммитится, руками не правится — страж «выгрузка = файл» (extract-icons.test.mjs).
// Дорисованное — packages/icons/icons.extra.json, вне этой выгрузки.
//
// Иконка — <svg width="46"> с подписью из NAMES (первый <div> после </svg>); прочие <svg> (лесенка
// размеров, кейлайны, образцы по 40 px) выгрузка пропускает молча, а <svg width="46"> с незнакомой
// подписью — ошибка. Каждая фигура (rect, circle, ellipse, line, polyline, polygon, path) даёт слой с
// контуром `d`: заливка и обводка одной фигуры — два слоя (сначала заливка, как рисует SVG). Атрибуты
// stroke, fill, stroke-width, opacity, stroke-linecap, transform наследуются от <g>; transform
// запекается в координаты. Роль слоя — по цвету (COLORS), незнакомый цвет — ошибка, а не «предмет»:
// пакет общий с вебом, страж «выгрузка = файл» закрепил бы порчу. Порядок слоёв сохраняется.
// `$optical` — множитель толщины контура на размер из лесенки мяча (16–48 px, U5).
import { readFileSync, writeFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const ARTBOARD = new URL('../screens/14-Иконки.dc.html', import.meta.url);
export const OUTPUT = new URL('../../packages/icons/icons.json', import.meta.url);

// Подпись артборда → ключ пакета.
export const NAMES = {
  мяч: 'ball',
  поле: 'pitch',
  ворота: 'goal',
  бутсы: 'boots',
  форма: 'kit',
  манишка: 'bib',
  свисток: 'whistle',
  время: 'time',
  угловой: 'corner',
  карточка: 'card',
  счёт: 'score',
  тренировка: 'training',
  состав: 'lineup',
  капитан: 'captain',
  команда: 'team',
  турнир: 'tournament',
  достижение: 'achievement',
  серия: 'streak',
  аренда: 'rent',
  'моя доля': 'my-share',
  расписание: 'schedule',
  площадка: 'venue',
  карта: 'map',
  уведомления: 'notifications',
  поиск: 'search',
  фильтр: 'filter',
  закрытый: 'private',
  позвать: 'invite',
  статистика: 'stats',
  комментарии: 'comments',
  замена: 'substitution',
  настройки: 'settings',
};

// Цвет артборда → роль слоя (U25), исчерпывающе. #5E6B64 — приглушённая внутренняя линия: тот же
// detail с прозрачностью, при которой #7E8C83 на bg тёмной темы (#0B0F0D) даёт #5E6B64 (±1 по каналу).
export const COLORS = {
  '#C3D0C7': { role: 'object' },
  '#7E8C83': { role: 'detail' },
  '#5E6B64': { role: 'detail', opacity: 0.73 },
  '#C6F24E': { role: 'accent' },
  '#8FA83A': { role: 'accent-detail' },
  '#0B0F0D': { role: 'knockout' },
};

const ACCENTS = new Set(['accent', 'accent-detail']);
const SHAPES = new Set(['rect', 'circle', 'ellipse', 'line', 'polyline', 'polygon', 'path']);
const LADDER = [16, 20, 24, 32, 48];

// Числа — стабильным форматом: до 2 знаков, без хвостовых нулей и «-0».
export function num(n) {
  const r = Math.round(n * 100) / 100;
  return String(Object.is(r, -0) ? 0 : r);
}
const round = (n, digits) => Math.round(n * 10 ** digits) / 10 ** digits;

function attrs(src) {
  const out = {};
  for (const m of src.matchAll(/([a-zA-Z][a-zA-Z0-9:-]*)\s*=\s*"([^"]*)"/g)) out[m[1]] = m[2];
  return out;
}

// ——— аффинные преобразования ([a b c d e f], как matrix() в SVG) ———

const IDENTITY = [1, 0, 0, 1, 0, 0];
const mul = (m, n) => [
  m[0] * n[0] + m[2] * n[1],
  m[1] * n[0] + m[3] * n[1],
  m[0] * n[2] + m[2] * n[3],
  m[1] * n[2] + m[3] * n[3],
  m[0] * n[4] + m[2] * n[5] + m[4],
  m[1] * n[4] + m[3] * n[5] + m[5],
];
const apply = (m, x, y) => [m[0] * x + m[2] * y + m[4], m[1] * x + m[3] * y + m[5]];

export function parseTransform(src) {
  let m = IDENTITY;
  for (const t of src.matchAll(/([a-zA-Z]+)\s*\(([^)]*)\)/g)) {
    const a = t[2]
      .trim()
      .split(/[\s,]+/)
      .map(Number);
    let n;
    if (t[1] === 'translate') n = [1, 0, 0, 1, a[0], a[1] ?? 0];
    else if (t[1] === 'scale') n = [a[0], 0, 0, a[1] ?? a[0], 0, 0];
    else if (t[1] === 'matrix' && a.length === 6) n = a;
    else if (t[1] === 'rotate') {
      const r = (a[0] * Math.PI) / 180;
      const [cx, cy] = [a[1] ?? 0, a[2] ?? 0];
      n = mul(mul([1, 0, 0, 1, cx, cy], [Math.cos(r), Math.sin(r), -Math.sin(r), Math.cos(r), 0, 0]), [
        1,
        0,
        0,
        1,
        -cx,
        -cy,
      ]);
    } else throw new Error(`незнакомое преобразование ${t[0]}`);
    m = mul(m, n);
  }
  return m;
}

// ——— фигуры → контур ———

const nums = (s) => (s.match(/-?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?/gi) ?? []).map(Number);

function shapeToPath(tag, a) {
  const n = (k, d = 0) => (a[k] === undefined ? d : Number(a[k]));
  switch (tag) {
    case 'path':
      return a.d ?? '';
    case 'line':
      return `M${n('x1')} ${n('y1')} L${n('x2')} ${n('y2')}`;
    case 'polyline':
    case 'polygon': {
      const p = nums(a.points ?? '');
      const pairs = [];
      for (let i = 0; i + 1 < p.length; i += 2) pairs.push(`${p[i]} ${p[i + 1]}`);
      return `M${pairs.join(' L')}${tag === 'polygon' ? ' Z' : ''}`;
    }
    case 'circle':
    case 'ellipse': {
      const [cx, cy] = [n('cx'), n('cy')];
      const [rx, ry] = tag === 'circle' ? [n('r'), n('r')] : [n('rx'), n('ry')];
      return `M${cx - rx} ${cy} A${rx} ${ry} 0 1 0 ${cx + rx} ${cy} A${rx} ${ry} 0 1 0 ${cx - rx} ${cy} Z`;
    }
    case 'rect': {
      const [x, y, w, h] = [n('x'), n('y'), n('width'), n('height')];
      let rx = a.rx === undefined ? undefined : n('rx');
      let ry = a.ry === undefined ? rx : n('ry');
      rx = Math.min(rx ?? ry ?? 0, w / 2);
      ry = Math.min(ry ?? rx, h / 2);
      if (!rx || !ry) return `M${x} ${y} H${x + w} V${y + h} H${x} Z`;
      const arc = (ex, ey) => `A${rx} ${ry} 0 0 1 ${ex} ${ey}`;
      return [
        `M${x + rx} ${y}`,
        `H${x + w - rx}`,
        arc(x + w, y + ry),
        `V${y + h - ry}`,
        arc(x + w - rx, y + h),
        `H${x + rx}`,
        arc(x, y + h - ry),
        `V${y + ry}`,
        arc(x + rx, y),
        'Z',
      ].join(' ');
    }
    default:
      throw new Error(`незнакомая фигура <${tag}>`);
  }
}

// Контур с преобразованием: в абсолютные команды, точки — через матрицу; H/V → L. Дуги — только при
// повороте, переносе и равном масштабе (rx и ry масштабируются, угол дуги поворачивается).
function transformPath(d, m) {
  const det = m[0] * m[3] - m[1] * m[2];
  const sx = Math.hypot(m[0], m[1]);
  const sy = Math.hypot(m[2], m[3]);
  const conformal = det > 0 && Math.abs(sx - sy) < 1e-9;
  const angle = (Math.atan2(m[1], m[0]) * 180) / Math.PI;
  const out = [];
  let [x, y, x0, y0] = [0, 0, 0, 0];
  for (const seg of d.matchAll(/([MmLlHhVvCcQqAaZzSsTt])([^MmLlHhVvCcQqAaZzSsTt]*)/g)) {
    const cmd = seg[1];
    const a = nums(seg[2]);
    const up = cmd.toUpperCase();
    const rel = cmd !== up;
    const pt = (px, py) => (rel ? [x + px, y + py] : [px, py]);
    const put = (c, ...pairs) =>
      out.push(c + pairs.map(([px, py]) => apply(m, px, py).map(num).join(' ')).join(' '));
    if (up === 'Z') {
      out.push('Z');
      [x, y] = [x0, y0];
      continue;
    }
    if (up === 'S' || up === 'T') throw new Error(`команда ${cmd} с transform не поддержана`);
    const step = { M: 2, L: 2, H: 1, V: 1, C: 6, Q: 4, A: 7 }[up];
    for (let i = 0; i < a.length; i += step) {
      const v = a.slice(i, i + step);
      if (up === 'M' || up === 'L') {
        [x, y] = pt(v[0], v[1]);
        put(up === 'M' && i === 0 ? 'M' : 'L', [x, y]);
        if (up === 'M' && i === 0) [x0, y0] = [x, y];
      } else if (up === 'H') {
        x = rel ? x + v[0] : v[0];
        put('L', [x, y]);
      } else if (up === 'V') {
        y = rel ? y + v[0] : v[0];
        put('L', [x, y]);
      } else if (up === 'C') {
        const p = [pt(v[0], v[1]), pt(v[2], v[3]), pt(v[4], v[5])];
        put('C', ...p);
        [x, y] = p[2];
      } else if (up === 'Q') {
        const p = [pt(v[0], v[1]), pt(v[2], v[3])];
        put('Q', ...p);
        [x, y] = p[1];
      } else {
        if (!conformal) throw new Error('дуга с неравным масштабом или отражением не поддержана');
        const [ex, ey] = pt(v[5], v[6]);
        const [tx, ty] = apply(m, ex, ey);
        out.push(
          `A${num(v[0] * sx)} ${num(v[1] * sx)} ${num(v[2] + angle)} ${v[3]} ${v[4]} ${num(tx)} ${num(ty)}`,
        );
        [x, y] = [ex, ey];
      }
    }
  }
  return out.join(' ');
}

const normalize = (d) =>
  d
    .replace(/-?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?/gi, (s) => num(Number(s)))
    .replace(/\s+/g, ' ')
    .trim();

// ——— одна иконка ———

function role(color, where) {
  const known = COLORS[color.toUpperCase()];
  if (!known) throw new Error(`${where}: незнакомый цвет ${color} — добавьте его в таблицу COLORS`);
  return known;
}

function extractIcon(svg, name) {
  const root = attrs(svg.match(/^<svg([^>]*)>/)[1]);
  const viewBox = root['sc-camel-view-box'] ?? root.viewBox;
  if (!viewBox) throw new Error(`иконка ${name}: нет viewBox`);
  const base = {
    fill: root.fill ?? 'black',
    stroke: root.stroke ?? 'none',
    'stroke-width': root['stroke-width'] ?? '1',
    'stroke-linecap': root['stroke-linecap'] ?? 'butt',
    opacity: 1,
    matrix: IDENTITY,
  };
  const rootCap = base['stroke-linecap'];
  const stack = [base];
  const layers = [];
  const body = svg.replace(/^<svg[^>]*>/, '').replace(/<\/svg>$/, '');
  for (const t of body.matchAll(/<(\/?)([a-zA-Z]+)([^>]*?)(\/?)>/g)) {
    const [, close, tag, rest, selfClose] = t;
    if (close) {
      if (tag === 'g') stack.pop();
      continue;
    }
    const a = attrs(rest);
    const parent = stack.at(-1);
    const ctx = {
      fill: a.fill ?? parent.fill,
      stroke: a.stroke ?? parent.stroke,
      'stroke-width': a['stroke-width'] ?? parent['stroke-width'],
      'stroke-linecap': a['stroke-linecap'] ?? parent['stroke-linecap'],
      opacity: parent.opacity * (a.opacity === undefined ? 1 : Number(a.opacity)),
      matrix: a.transform ? mul(parent.matrix, parseTransform(a.transform)) : parent.matrix,
    };
    if (a['stroke-linejoin'] && a['stroke-linejoin'] !== root['stroke-linejoin']) {
      throw new Error(`иконка ${name}: stroke-linejoin="${a['stroke-linejoin']}" не поддержан`);
    }
    if (tag === 'g') {
      if (!selfClose) stack.push(ctx);
      continue;
    }
    if (!SHAPES.has(tag)) throw new Error(`иконка ${name}: незнакомый элемент <${tag}>`);
    const raw = shapeToPath(tag, a);
    const d = ctx.matrix === IDENTITY ? normalize(raw) : transformPath(raw, ctx.matrix);
    if (!d) throw new Error(`иконка ${name}: пустой контур <${tag}>`);
    const paints = [];
    if (ctx.fill !== 'none') paints.push(['fill', ctx.fill]);
    if (ctx.stroke !== 'none') paints.push(['stroke', ctx.stroke]);
    for (const [paint, color] of paints) {
      const r = role(color, `иконка ${name}`);
      const layer = { d, role: r.role, paint };
      if (paint === 'stroke') layer.width = Number(num(Number(ctx['stroke-width'])));
      const opacity = round(ctx.opacity * (r.opacity ?? 1), 2);
      if (opacity !== 1) layer.opacity = opacity;
      if (paint === 'stroke' && ctx['stroke-linecap'] !== rootCap) layer.cap = ctx['stroke-linecap'];
      if (r.role === 'knockout') {
        // вырез поверх акцентного слоя (рубль «аренды», пятиугольник в линзе «поиска») — text-inverse,
        // остальные — цветом поверхности (U25)
        const under = layers.findLast((l) => l.role !== 'knockout');
        layer.on = under && ACCENTS.has(under.role) ? 'accent' : 'surface';
      }
      layers.push(layer);
    }
  }
  if (layers.length === 0) throw new Error(`иконка ${name}: нет слоёв`);
  return { viewBox, layers };
}

// Лесенка мяча: <svg width="N"> с подписью N (16…48) — толщина контура на размер; множитель — к 48.
function extractOptical(html) {
  const widths = {};
  // тело — без вложенного <svg>: иначе ленивый поиск подписи перешагнул бы в соседний артборд
  const re = /<svg width="(\d+)"[^>]*>((?:(?!<svg)[\s\S])*?)<\/svg>\s*<div[^>]*>\s*(\d+)\s*<\/div>/g;
  for (const m of html.matchAll(re)) {
    if (m[1] !== m[3] || !LADDER.includes(Number(m[1]))) continue;
    const w = [...m[2].matchAll(/stroke-width="([^"]+)"/g)].map((x) => Number(x[1]));
    if (w.length === 0) throw new Error(`лесенка ${m[1]} px: нет толщины контура`);
    widths[m[1]] = w;
  }
  const master = widths['48'];
  if (!master) return undefined;
  const optical = {};
  for (const size of LADDER) {
    const w = widths[String(size)];
    if (!w) throw new Error(`лесенка: нет размера ${size} px`);
    const k = w[0] / master[0];
    // множитель общий для всех слоёв (U5): внутренняя линия растёт так же, как контур
    w.forEach((v, i) => {
      if (master[i] !== undefined && Math.abs(v - master[i] * k) > 0.01) {
        throw new Error(`лесенка ${size} px: слой ${i + 1} растёт не тем же множителем`);
      }
    });
    optical[size] = round(k, 4);
  }
  return optical;
}

export function extractIcons(html) {
  const icons = {};
  const re = /(<svg width="46"[^>]*>(?:(?!<svg)[\s\S])*?<\/svg>)\s*<div[^>]*>([^<]*)<\/div>/g;
  for (const m of html.matchAll(re)) {
    const label = m[2].trim();
    const name = Object.hasOwn(NAMES, label) ? NAMES[label] : undefined;
    if (!name) throw new Error(`незнакомая подпись иконки «${label}» — добавьте её в таблицу NAMES`);
    if (icons[name]) throw new Error(`иконка ${name} («${label}») встречается дважды`);
    icons[name] = extractIcon(m[1], name);
  }
  const optical = extractOptical(html);
  const out = {};
  if (optical) out.$optical = optical;
  for (const k of Object.keys(icons).sort()) out[k] = icons[k];
  return out;
}

export function buildIconsJson(html) {
  const icons = extractIcons(html);
  if (!icons.$optical) throw new Error('в артборде нет лесенки размеров мяча (16–48 px)');
  return `${JSON.stringify(icons, null, 2)}\n`;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const json = buildIconsJson(readFileSync(ARTBOARD, 'utf8'));
  writeFileSync(OUTPUT, json);
  const count = Object.keys(JSON.parse(json)).filter((k) => !k.startsWith('$')).length;
  console.log(`иконок: ${count} → packages/icons/icons.json`);
}
