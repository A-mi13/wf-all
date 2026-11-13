import { ICONS, type IconLayer, type IconName, iconGroup, OPTICAL } from '@wf/icons';
import { cx } from '../cx';
import styles from './Icon.module.css';

// Иконка из @wf/icons (спека UI-фундамента §4.9, план UF1a.3). Без label — декоративная (aria-hidden,
// не получает фокус), с label — role="img" и имя. Толщина контура — атрибутом stroke-width (данные, не
// стиль — CSP): у иконок артборда и дорисованных — ширина слоя × OPTICAL[size] (оптическая коррекция
// «лесенки» артборда 14), у иконок меню (nav) — абсолютная ширина слоя, вид меню прежний (m1). Цвет —
// классами ролей (Icon.module.css): предмет — currentColor. 12 — ниже нижней ступени «лесенки» (16): линия держит
// толщину 16 px в пикселях (OPTICAL[16] × 16/12), а не истончается (крестик чипа MultiSelectField).
export type IconSize = 12 | 16 | 20 | 24;

const optical = (size: IconSize) => (size === 12 ? OPTICAL[16] * (16 / 12) : OPTICAL[size]);

const ROLE_CLASS: Record<IconLayer['role'], string | undefined> = {
  object: styles.object,
  detail: styles.detail,
  accent: styles.accent,
  'accent-detail': styles.accentDetail,
  knockout: styles.knockout,
};

const round2 = (n: number) => Math.round(n * 100) / 100;

// Общий рендер Icon и Illustration: слои по порядку (акцент поверх предмета).
export function IconGraphic({
  name,
  size,
  scale,
  objectClass,
  label,
  className,
}: {
  name: IconName;
  size: number;
  scale: number;
  objectClass: string | undefined;
  label?: string;
  className?: string;
}) {
  const def = ICONS[name];
  const a11y = label
    ? { role: 'img', 'aria-label': label }
    : { 'aria-hidden': true as const, focusable: false as const };
  return (
    <svg
      viewBox={def.viewBox}
      width={size}
      height={size}
      className={cx(styles.svg, className)}
      {...a11y}
    >
      {def.layers.map((l, i) => {
        const stroke = l.paint === 'stroke';
        return (
          <path
            key={i}
            d={l.d}
            className={cx(
              l.role === 'object' ? objectClass : ROLE_CLASS[l.role],
              stroke ? styles.stroke : styles.fill,
              l.on === 'accent' && styles.onAccent,
            )}
            fill={stroke ? 'none' : undefined}
            strokeWidth={stroke ? round2((l.width ?? 1) * scale) : undefined}
            strokeLinecap={l.cap}
            opacity={l.opacity}
          />
        );
      })}
    </svg>
  );
}

export function Icon({
  name,
  size = 20,
  label,
  className,
}: {
  name: IconName;
  size?: IconSize;
  label?: string;
  className?: string;
}) {
  const scale = iconGroup(name) === 'nav' ? 1 : optical(size);
  return (
    <IconGraphic
      name={name}
      size={size}
      scale={scale}
      objectClass={styles.object}
      label={label}
      className={className}
    />
  );
}
