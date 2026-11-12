// Комбинации горячих клавиш (спека UI-фундамента §4.2): строка «mod+k», «g c», «/», «?» разбирается в шаги по
// KeyboardEvent.code — физической клавише, а не символу: в русской раскладке «g» — это «п», а code тот же (KeyG).
// mod — Ctrl или ⌘; шаги через пробел — последовательность (следующий шаг — в пределах SEQUENCE_MS).

export const SEQUENCE_MS = 1000;

export type Step = {
  code: string;
  mod: boolean;
  shift: boolean;
  alt: boolean;
  token: string; // как записано в комбинации (латиница) — запасная подпись в шпаргалке
};

export type Combo = {
  key: string; // нормализованная запись: по ней — совпадение и конфликт
  steps: Step[];
  single: boolean; // без Ctrl/⌘ и Alt — односимвольная (WCAG 2.1.4: выключаемая)
};

// Клавиши-символы: code и подразумеваемый Shift (? — Shift и клавиша «/»).
const SYMBOLS: Record<string, { code: string; shift?: boolean }> = {
  '/': { code: 'Slash' },
  '?': { code: 'Slash', shift: true },
  ',': { code: 'Comma' },
  '.': { code: 'Period' },
  escape: { code: 'Escape' },
  enter: { code: 'Enter' },
};
const MODIFIERS = new Set(['mod', 'shift', 'alt']);

function keyOf(token: string): { code: string; shift?: boolean } {
  if (/^[a-z]$/.test(token)) return { code: `Key${token.toUpperCase()}` };
  if (/^[0-9]$/.test(token)) return { code: `Digit${token}` };
  const symbol = SYMBOLS[token];
  if (!symbol) throw new Error(`useShortcut: незнакомая клавиша «${token}»`);
  return symbol;
}

const stepKey = (s: { code: string; mod: boolean; shift: boolean; alt: boolean }) =>
  `${s.mod ? 'mod+' : ''}${s.alt ? 'alt+' : ''}${s.shift ? 'shift+' : ''}${s.code}`;

export function parseCombo(combo: string): Combo {
  const steps = combo
    .trim()
    .split(/\s+/)
    .map((raw): Step => {
      const parts = raw.toLowerCase().split('+');
      const token = parts.pop() ?? '';
      for (const m of parts) {
        if (!MODIFIERS.has(m)) throw new Error(`useShortcut: незнакомый модификатор «${m}»`);
      }
      const { code, shift } = keyOf(token);
      return {
        code,
        mod: parts.includes('mod'),
        shift: Boolean(shift) || parts.includes('shift'),
        alt: parts.includes('alt'),
        token,
      };
    });
  return {
    key: steps.map(stepKey).join(' '),
    steps,
    single: steps.every((s) => !s.mod && !s.alt),
  };
}

// Платформа Apple: mod — ⌘, иначе — Ctrl (ревью UF4-K m4: Ctrl+K в полях macOS — «удалить до конца строки»).
export function isApplePlatform(): boolean {
  return /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
}

// Нажатие — в той же записи, что шаг комбинации. «Чужой» модификатор (Ctrl на Mac, ⌘/Win на остальных) — свой
// префикс, которого нет ни в одной комбинации: такое нажатие не совпадает ни с чем (и не считается односимвольным).
export const eventKey = (e: KeyboardEvent): string => {
  const foreign = isApplePlatform() ? e.ctrlKey : e.metaKey;
  const key = stepKey({
    code: e.code,
    mod: e.ctrlKey || e.metaKey,
    shift: e.shiftKey,
    alt: e.altKey,
  });
  return foreign ? `other+${key}` : key;
};

// Сами модификаторы шагом не считаются (Shift между «g» и «c» последовательность не рвёт).
export const isModifierKey = (e: KeyboardEvent): boolean =>
  /^(Control|Shift|Alt|Meta|OS)(Left|Right)?$/.test(e.code) ||
  ['Control', 'Shift', 'Alt', 'Meta'].includes(e.key);

// Фокус в поле ввода — односимвольные не срабатывают (кроме allowInInput).
const EDITABLE =
  'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="combobox"], [role="textbox"]';
export const isEditableTarget = (target: EventTarget | null): boolean =>
  target instanceof Element && target.closest(EDITABLE) !== null;
