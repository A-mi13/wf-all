// Общая логика локализации веба и админки.
// use-intl считает "" найденным сообщением, поэтому наивный merge ru+en показал бы пустоту.

export type Messages = { [key: string]: string | Messages };

const isGroup = (v: unknown): v is Messages => typeof v === 'object' && v !== null;

export function withFallback(base: Messages, over: Messages): Messages {
  const out: Messages = { ...base };
  for (const [key, value] of Object.entries(over)) {
    if (typeof value === 'string') {
      if (value !== '') Object.defineProperty(out, key, { value, enumerable: true, writable: true, configurable: true });
    } else {
      const b = base[key];
      const merged = withFallback(isGroup(b) ? b : {}, value);
      Object.defineProperty(out, key, { value: merged, enumerable: true, writable: true, configurable: true });
    }
  }
  return out;
}

function paths(m: Messages, prefix = ''): string[] {
  return Object.entries(m).flatMap(([k, v]) =>
    isGroup(v) ? paths(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  );
}

export function diffKeys(a: Messages, b: Messages): { missing: string[]; extra: string[] } {
  const pa = new Set(paths(a));
  const pb = new Set(paths(b));
  return {
    missing: [...pa].filter((p) => !pb.has(p)).sort(),
    extra: [...pb].filter((p) => !pa.has(p)).sort(),
  };
}
