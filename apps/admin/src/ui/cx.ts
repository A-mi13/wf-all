// Склейка классов CSS Modules: ложные значения пропускаются.
export function cx(...names: (string | false | null | undefined)[]): string {
  return names.filter(Boolean).join(' ');
}
