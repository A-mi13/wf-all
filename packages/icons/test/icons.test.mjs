import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

// Пакет @wf/icons (спека UI-фундамента §4.9, план UF1a.2, U6, U25): три источника — выгрузка артборда
// (icons.json, страж «выгрузка = файл» — design/tools/extract-icons.test.mjs), меню (nav, макет 08) и
// дорисованные (drawn, кандидаты в артборд, строки «дорисовано *» в design/README.md).
const read = (p) => readFileSync(new URL(p, import.meta.url), 'utf8');
const artboard = JSON.parse(read('../icons.json'));
const extra = JSON.parse(read('../icons.extra.json'));
const readme = read('../../../design/README.md');

const ROLES = ['object', 'detail', 'accent', 'accent-detail', 'knockout'];
const names = (o) => Object.keys(o).filter((k) => !k.startsWith('$'));
const sources = { artboard: artboard, nav: extra.nav, drawn: extra.drawn };
const all = Object.entries(sources).flatMap(([group, icons]) =>
  names(icons).map((name) => ({ group, name, def: icons[name] })),
);

test('в icons.extra.json ровно две группы — nav и drawn', () => {
  assert.deepEqual(Object.keys(extra).sort(), ['drawn', 'nav']);
});

test('ключи трёх источников не пересекаются', () => {
  const seen = new Map();
  const clash = [];
  for (const { group, name } of all) {
    if (seen.has(name)) clash.push(`${name}: ${seen.get(name)} и ${group}`);
    seen.set(name, group);
  }
  assert.deepEqual(clash, []);
});

test('у каждого слоя непустой d, роль и paint из перечня; толщина — у обводки', () => {
  const bad = [];
  for (const { group, name, def } of all) {
    if (!/^0 0 \d+ \d+$/.test(def.viewBox)) bad.push(`${group}/${name}: viewBox ${def.viewBox}`);
    if (!Array.isArray(def.layers) || def.layers.length === 0)
      bad.push(`${group}/${name}: нет слоёв`);
    for (const [i, l] of (def.layers ?? []).entries()) {
      const at = `${group}/${name}[${i}]`;
      if (typeof l.d !== 'string' || l.d.trim() === '') bad.push(`${at}: пустой d`);
      if (!ROLES.includes(l.role)) bad.push(`${at}: роль ${l.role}`);
      if (l.paint !== 'stroke' && l.paint !== 'fill') bad.push(`${at}: paint ${l.paint}`);
      if (l.paint === 'stroke' && !(l.width > 0)) bad.push(`${at}: обводка без width`);
      if (l.opacity !== undefined && !(l.opacity > 0 && l.opacity < 1))
        bad.push(`${at}: opacity ${l.opacity}`);
      if ((l.role === 'knockout') !== (l.on !== undefined)) bad.push(`${at}: on только у knockout`);
      if (l.on !== undefined && l.on !== 'surface' && l.on !== 'accent')
        bad.push(`${at}: on ${l.on}`);
    }
  }
  assert.deepEqual(bad, []);
});

test('каждая дорисованная иконка — строкой «дорисовано *» в design/README.md', () => {
  const lines = readme.split('\n').filter((l) => l.includes('дорисовано *'));
  const missing = names(extra.drawn).filter((k) => !lines.some((l) => l.includes(`\`${k}\``)));
  assert.deepEqual(missing, []);
});

test('$optical — множители для 16, 20, 24 и 48 px; у 48 — 1', () => {
  const o = artboard.$optical;
  for (const size of ['16', '20', '24', '48'])
    assert.equal(typeof o?.[size], 'number', `нет ${size}`);
  assert.equal(o['48'], 1);
  assert.equal(o['20'], 1.25);
});

test('иконки меню — сетка 24, один слой-предмет с абсолютной толщиной 2.2 (вид меню не меняется)', () => {
  for (const [name, def] of Object.entries(extra.nav)) {
    assert.equal(def.viewBox, '0 0 24 24', name);
    assert.deepEqual(
      def.layers.map((l) => [l.role, l.paint, l.width]),
      [['object', 'stroke', 2.2]],
      name,
    );
  }
});
