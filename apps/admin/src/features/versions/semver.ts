// Версии приложения MAJOR.MINOR.PATCH (контракт AppVersionsPut) — разбор и сравнение для формы «Версии приложения».

// MAJOR.MINOR.PATCH без ведущих нулей и сборки.
export const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

// Сравнение версий (обе — по SEMVER) числами, не строками: < 0, 0, > 0.
export function compareVersions(a: string, b: string): number {
  const pa = a.split('.').map(Number);
  const pb = b.split('.').map(Number);
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}

// Подъём минимальной версии — внешнее последствие (m-4): клиенты ниже неё получат требование обновиться. Первая
// запись минимальной — тоже подъём (раньше проверки не было).
export const raisesMin = (prev: string | null, next: string) =>
  prev === null || !SEMVER.test(prev) || compareVersions(next, prev) > 0;
