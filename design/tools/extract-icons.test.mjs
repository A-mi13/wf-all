import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { ARTBOARD, OUTPUT, buildIconsJson, extractIcons } from './extract-icons.mjs';

// Фрагмент артборда 14: карточка иконки — <svg width="46"> и подпись следующим <div>.
const SVG =
  '<svg width="46" height="46" sc-camel-view-box="0 0 48 48" fill="none" stroke-linecap="round" ' +
  'stroke-linejoin="round" aria-hidden="true">';
const card = (label, body) =>
  `<div>${SVG}${body}</svg>\n<div style="font-size: 10.5px">${label}</div><div>описание</div></div>`;

test('примитивы и наследование от <g>: d, роль по цвету, paint, width, opacity', () => {
  const icons = extractIcons(
    card(
      'мяч',
      '<rect x="5" y="10" width="38" height="28" rx="3" stroke="#C3D0C7" stroke-width="2.4"></rect>' +
        '<g stroke="#7E8C83" stroke-width="1.1" opacity="0.7"><circle cx="24" cy="24" r="5"></circle></g>' +
        '<polygon points="24,18 29.71,22.15 27.53,28.85" fill="#C6F24E" stroke="none"></polygon>',
    ),
  );
  assert.deepEqual(icons.ball, {
    viewBox: '0 0 48 48',
    layers: [
      {
        d: 'M8 10 H40 A3 3 0 0 1 43 13 V35 A3 3 0 0 1 40 38 H8 A3 3 0 0 1 5 35 V13 A3 3 0 0 1 8 10 Z',
        role: 'object',
        paint: 'stroke',
        width: 2.4,
      },
      {
        d: 'M19 24 A5 5 0 1 0 29 24 A5 5 0 1 0 19 24 Z',
        role: 'detail',
        paint: 'stroke',
        width: 1.1,
        opacity: 0.7,
      },
      { d: 'M24 18 L29.71 22.15 L27.53 28.85 Z', role: 'accent', paint: 'fill' },
    ],
  });
});

test('заливка и обводка одной фигуры — два слоя, заливка первой; #8FA83A — accent-detail', () => {
  const icons = extractIcons(
    card('манишка', '<path d="M16 11 h16 v3.5 z" fill="#C6F24E" stroke="#8FA83A" stroke-width="2"></path>'),
  );
  assert.deepEqual(
    icons.bib.layers.map((l) => [l.role, l.paint, l.width]),
    [
      ['accent', 'fill', undefined],
      ['accent-detail', 'stroke', 2],
    ],
  );
});

test('#0B0F0D — knockout; на поверхности — on: surface, после акцентного слоя — on: accent', () => {
  const icons = extractIcons(
    card(
      'фильтр',
      '<path d="M7 11 h34" stroke="#C3D0C7" stroke-width="2.4"></path>' +
        '<circle cx="35" cy="33" r="9" fill="#0B0F0D" stroke="none"></circle>' +
        '<circle cx="35" cy="33" r="7" fill="#C6F24E" stroke="none"></circle>' +
        '<path d="M31.8 33 l2.3 2.3" stroke="#0B0F0D" stroke-width="2" fill="none"></path>' +
        '<path d="M20 20 L21 21" stroke="#7E8C83" stroke-width="1.5"></path>',
    ),
  );
  assert.deepEqual(
    icons.filter.layers.map((l) => [l.role, l.on]),
    [
      ['object', undefined],
      ['knockout', 'surface'],
      ['accent', undefined],
      ['knockout', 'accent'],
      ['detail', undefined],
    ],
  );
});

test('#5E6B64 — detail с прозрачностью (и умножается на opacity группы); butt — cap слоя', () => {
  const icons = extractIcons(
    card(
      'поле',
      '<g stroke-linecap="butt" opacity="0.5"><path d="M24 10 V38" stroke="#5E6B64" stroke-width="1.4"></path></g>',
    ),
  );
  assert.deepEqual(icons.pitch.layers, [
    { d: 'M24 10 V38', role: 'detail', paint: 'stroke', width: 1.4, opacity: 0.37, cap: 'butt' },
  ]);
});

test('transform группы запекается в координаты; числа — до 2 знаков', () => {
  const icons = extractIcons(
    card(
      'карточка',
      '<g transform="rotate(90 24 24)"><line x1="10" y1="24" x2="20" y2="24.333333" stroke="#C3D0C7" stroke-width="2"></line></g>',
    ),
  );
  assert.equal(icons.card.layers[0].d, 'M24 10 L23.67 20');
});

test('line, polyline, ellipse', () => {
  const icons = extractIcons(
    card(
      'счёт',
      '<line x1="1" y1="2" x2="3" y2="4" stroke="#C3D0C7"></line>' +
        '<polyline points="1,1 2,2 3,1" stroke="#C3D0C7" stroke-width="2"></polyline>' +
        '<ellipse cx="10" cy="10" rx="4" ry="2" fill="#C6F24E"></ellipse>',
    ),
  );
  assert.deepEqual(
    icons.score.layers.map((l) => l.d),
    ['M1 2 L3 4', 'M1 1 L2 2 L3 1', 'M6 10 A4 2 0 1 0 14 10 A4 2 0 1 0 6 10 Z'],
  );
  assert.equal(icons.score.layers[0].width, 1, 'stroke-width по умолчанию — 1');
});

test('незнакомый цвет — ошибка с цветом и именем иконки', () => {
  assert.throws(
    () => extractIcons(card('мяч', '<circle cx="1" cy="1" r="1" stroke="#123456"></circle>')),
    /иконка ball: незнакомый цвет #123456/,
  );
});

test('незнакомая подпись — ошибка с её текстом; прочие svg (не 46) пропускаются молча', () => {
  assert.throws(() => extractIcons(card('вертолёт', '')), /незнакомая подпись иконки «вертолёт»/);
  const other = '<svg width="40" height="40"><circle r="1" stroke="#ABCDEF"></circle></svg><div>мяч</div>';
  assert.deepEqual(extractIcons(other), {});
});

test('незнакомый элемент внутри иконки — ошибка', () => {
  assert.throws(
    () => extractIcons(card('мяч', '<text x="1" y="1" fill="#C3D0C7">C</text>')),
    /незнакомый элемент <text>/,
  );
});

test('лесенка размеров → $optical: множитель к 48 px, общий для слоёв', () => {
  const step = (n, a, b) =>
    `<svg width="${n}" height="${n}"><circle stroke-width="${a}"></circle><g stroke-width="${b}"></g></svg><div>${n}</div>`;
  const html = [
    step(16, 3.4, 2.38),
    step(20, 3, 2.1),
    step(24, 2.8, 1.96),
    step(32, 2.6, 1.82),
    step(48, 2.4, 1.68),
  ].join('');
  assert.deepEqual(extractIcons(html).$optical, { 16: 1.4167, 20: 1.25, 24: 1.1667, 32: 1.0833, 48: 1 });
  assert.throws(() => extractIcons(html.replace('2.1"', '2.5"')), /20 px: слой 2/);
});

// Страж «руками не правится» (как у токенов): закоммиченный icons.json = выгрузка артборда.
test('выгрузка настоящего артборда совпадает с packages/icons/icons.json', () => {
  const json = buildIconsJson(readFileSync(ARTBOARD, 'utf8'));
  assert.equal(readFileSync(OUTPUT, 'utf8'), json, 'запусти node design/tools/extract-icons.mjs');
  const names = Object.keys(JSON.parse(json)).filter((k) => !k.startsWith('$'));
  assert.equal(names.length, 32);
});
