// Типы contrast.mjs для потребителей на TypeScript (страж CSS админки, apps/admin/src/ui/contrast-css.test.ts).
export type ThemeName = 'light' | 'dark';
export type TokenColor = string | { readonly dark: string; readonly light: string };

export function relativeLuminance(hex: string): number;
export function contrastRatio(a: string, b: string): number;
export function colorOf(
  tokens: { readonly color: Readonly<Record<string, TokenColor>> },
  name: string,
  theme: ThemeName,
): string;
export function mixSrgb(a: string, b: string, p: number): string;
