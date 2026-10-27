import { useMemo } from 'react';
import { encode } from 'uqr';
import styles from './QrCode.module.css';

// QR-код на клиенте (спека §9.1, R11): матрица — uqr (без сети и зависимостей), рисуется SVG-путём
// без inline-стилей (CSP style-src 'self'), цвета — токены в CSS. Значение (ссылка otpauth:// с
// секретом) никуда не уходит. Светлый фон и тёмные модули — сканеры не читают инверсию.
export function QrCode({ value, label }: { value: string; label: string }) {
  const { size, d } = useMemo(() => {
    const qr = encode(value, { ecc: 'M', border: 2 });
    // модуль — квадрат 1×1 в координатах матрицы
    let path = '';
    qr.data.forEach((row, y) =>
      row.forEach((dark, x) => {
        if (dark) path += `M${x} ${y}h1v1h-1z`;
      }),
    );
    return { size: qr.size, d: path };
  }, [value]);
  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${size} ${size}`}
      shapeRendering="crispEdges"
      className={styles.qr}
    >
      <rect width={size} height={size} className={styles.light} />
      <path d={d} className={styles.dark} />
    </svg>
  );
}
