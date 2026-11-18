// Причина доступа к ПД игрока (заголовок X-WF-Access-Reason; контакты заявителя, поиск игрока — план I-4, R19, R41).
// Сервер после декодирования требует 3–200 символов и отвергает управляющие и невидимые символы (422 format):
// клиент проверяет то же до запроса и пишет понятный текст под полем. Безопасные невидимые — мягкий перенос
// U+00AD, нулевой ширины и метки направления U+200B–U+200F (ZWSP, ZWNJ, ZWJ, LRM, RLM), BOM U+FEFF — не меняют
// смысла, их клиент срезает сам; остальные управляющие (Cc), форматные (Cf: bidi-переопределения и изоляции) и
// разделители строк и абзацев (Zl, Zp) — ошибка поля: сотрудник видит, что именно не так. Причина — только в
// памяти диалога: ни адреса, ни storage, ни ключа кэша.

export const REASON_MIN = 3;
export const REASON_MAX = 200;

const SAFE_INVISIBLE = /[\u00AD\u200B-\u200F\uFEFF]/gu;
const FORBIDDEN = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;

export function cleanAccessReason(raw: string): string {
  return raw.replace(SAFE_INVISIBLE, '').trim();
}

// Правило, которое нарушает очищенная причина, или null. Длина — в символах (кодовых точках), как у сервера.
export function reasonProblem(reason: string): 'format' | 'minLength' | 'maxLength' | null {
  if (FORBIDDEN.test(reason)) return 'format';
  const length = [...reason].length;
  if (length < REASON_MIN) return 'minLength';
  if (length > REASON_MAX) return 'maxLength';
  return null;
}

// Значение заголовка: UTF-8 в percent-encoding (контракт AccessReason; сырая кириллица — 400).
export const encodeAccessReason = (reason: string) => encodeURIComponent(reason);
