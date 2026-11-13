// Иконки и иллюстрации веба и админки (спека UI-фундамента §4.9, план UF1a.2): данные, без рендера —
// React-компонент у каждого приложения свой (Icon/Illustration админки — apps/admin/src/ui/icon).
// Три источника без пересечения ключей (страж test/icons.test.mjs):
// - icons.json — выгрузка артборда 14 (design/tools/extract-icons.mjs), руками не править;
// - icons.extra.json, группа nav — иконки меню из макета 08 (сетка 24, абсолютная толщина контура);
// - icons.extra.json, группа drawn — дорисованные *, строки в design/README.md.
import artboard from './icons.json';
import extra from './icons.extra.json';

// Роль слоя → цвет у приложения (U7, U25): предмет, внутренняя линия, акцент, тень акцента, вырез по фону.
export type IconRole = 'object' | 'detail' | 'accent' | 'accent-detail' | 'knockout';

export type IconLayer = {
  d: string;
  role: IconRole;
  paint: 'stroke' | 'fill';
  // толщина обводки: у артборда и drawn — в единицах сетки 48 (× OPTICAL на размер), у nav — абсолютная
  width?: number;
  opacity?: number;
  // окончание линии, если не round (корень иконки рисует round)
  cap?: 'butt' | 'square';
  // только у knockout: вырез поверх акцентного слоя рисуется text-inverse, иначе — цветом поверхности
  on?: 'surface' | 'accent';
};

export type IconDef = { viewBox: string; layers: readonly IconLayer[] };

type ArtboardName = Exclude<keyof typeof artboard, `$${string}`>;
export type IconName = ArtboardName | keyof typeof extra.nav | keyof typeof extra.drawn;
export type IconGroup = 'artboard' | 'nav' | 'drawn';
export type IconSize = 16 | 20 | 24 | 32 | 48;

const { $optical, ...fromArtboard } = artboard;

// Множитель толщины контура на размер из «лесенки» мяча (U5): общий для всех слоёв; 48 — 1.
export const OPTICAL = $optical as Readonly<Record<IconSize, number>>;

export const ICONS = {
  ...fromArtboard,
  ...extra.nav,
  ...extra.drawn,
} as unknown as Readonly<Record<IconName, IconDef>>;

const NAV = new Set<string>(Object.keys(extra.nav));
const DRAWN = new Set<string>(Object.keys(extra.drawn));

// Источник иконки: nav рисуется с абсолютной толщиной (вид меню не меняется), остальные — с OPTICAL.
export function iconGroup(name: IconName): IconGroup {
  if (NAV.has(name)) return 'nav';
  return DRAWN.has(name) ? 'drawn' : 'artboard';
}
