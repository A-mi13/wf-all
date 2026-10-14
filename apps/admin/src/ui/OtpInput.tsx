import {
  type ClipboardEvent,
  type KeyboardEvent,
  type Ref,
  useId,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react';
import { useTranslations } from 'use-intl';
import { cx } from './cx';
import styles from './OtpInput.module.css';

const LENGTH = 6;
const EMPTY: string[] = Array.from({ length: LENGTH }, () => '');
const isDigit = (s: string) => s.length === 1 && s >= '0' && s <= '9';
const digitsOf = (s: string) => s.replace(/\D/g, '');

export type OtpInputHandle = {
  reset(): void; // после ошибки кода: ячейки пусты (и onChange('')), фокус на первой
  focus(): void;
};

export type OtpInputProps = {
  label: string; // подпись группы: «Код из приложения-аутентификатора»
  onComplete: (code: string) => void; // все 6 цифр введены — автоотправка
  onChange?: (code: string) => void; // текущие цифры подряд (неполный код — короче 6)
  invalid?: boolean; // код не подошёл: рамки ошибки, aria-invalid
  disabled?: boolean; // запрос идёт
  autoFocus?: boolean;
  describedBy?: string; // id текста ошибки или подсказки под ячейками
  ref?: Ref<OtpInputHandle>;
};

// Ввод кода второго фактора (спека §9.4, Review Focus 5): 6 ячеек по одной цифре.
// - Принимаются только цифры: прочее при вводе, вставке и автозаполнении отбрасывается.
// - Вставка из буфера в любом виде («123 456», «123-456», с переводом строки): 6 цифр и больше —
//   с первой ячейки, меньше — с текущей. Автозаполнение кода из СМС/связки ключей (весь код в
//   одну ячейку) — так же.
// - Полный код уходит в onComplete один раз на ввод; после reset() — снова.
// - Backspace: в заполненной ячейке стирает её, в пустой — уходит назад и стирает предыдущую.
//   Стрелки, Home, End — переход между ячейками.
// Режим «резервный код» — обычное текстовое поле на экране входа (Task 13), не здесь.
export function OtpInput({
  label,
  onComplete,
  onChange,
  invalid,
  disabled,
  autoFocus,
  describedBy,
  ref,
}: OtpInputProps) {
  const t = useTranslations('ui.otp');
  const id = useId();
  const [cells, setCells] = useState<string[]>(EMPTY);
  const inputs = useRef<(HTMLInputElement | null)[]>([]);

  const focusAt = (i: number) => {
    const el = inputs.current[Math.max(0, Math.min(LENGTH - 1, i))];
    el?.focus();
    el?.select();
  };

  // Фокус, отложенный до включения ячеек: onError мутации зовёт reset(), пока ячейки ещё
  // disabled (isPending снимается позже — оповещения query-core идут через setTimeout 0).
  const pendingFocus = useRef<number | null>(null);
  const requestFocus = (i: number) => {
    const el = inputs.current[i];
    if (el && !el.disabled) focusAt(i);
    else pendingFocus.current = i;
  };
  useEffect(() => {
    if (disabled || pendingFocus.current === null) return;
    focusAt(pendingFocus.current);
    pendingFocus.current = null;
  });

  useImperativeHandle(ref, () => ({
    reset() {
      setCells(EMPTY);
      // экран держит текущий код (onChange) — пустые ячейки должны дойти и до него, иначе
      // кнопка отправки повторит стёртый код
      onChange?.('');
      requestFocus(0);
    },
    focus() {
      // после reset() в том же такте cells ещё старые — ждём, если уже ждём первую
      if (pendingFocus.current !== null) return;
      const empty = cells.findIndex((c) => !c);
      requestFocus(empty === -1 ? LENGTH - 1 : empty);
    },
  }));

  // Новое состояние ячеек: сообщить наружу; полный код — в onComplete (из обработчика события,
  // не из эффекта: один ввод — один вызов).
  const commit = (next: string[]) => {
    setCells(next);
    const code = next.join('');
    onChange?.(code);
    if (next.every(isDigit)) onComplete(code);
  };

  // Несколько цифр сразу (вставка, автозаполнение): полный код — с первой ячейки.
  const fill = (from: number, raw: string) => {
    const digits = digitsOf(raw);
    if (!digits) return;
    const start = digits.length >= LENGTH ? 0 : from;
    const next = [...cells];
    for (let k = 0; k < digits.length && start + k < LENGTH; k++) next[start + k] = digits[k]!;
    commit(next);
    focusAt(Math.min(start + digits.length, LENGTH - 1));
  };

  const setAt = (i: number, digit: string) => {
    const next = [...cells];
    next[i] = digit;
    commit(next);
  };

  const onKeyDown = (i: number, e: KeyboardEvent<HTMLInputElement>) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return; // Ctrl+V и прочие сочетания — браузеру
    const k = e.key;
    if (isDigit(k)) {
      e.preventDefault();
      setAt(i, k);
      if (i < LENGTH - 1) focusAt(i + 1);
    } else if (k === 'Backspace') {
      e.preventDefault();
      if (cells[i]) setAt(i, '');
      else if (i > 0) {
        setAt(i - 1, '');
        focusAt(i - 1);
      }
    } else if (k === 'Delete') {
      e.preventDefault();
      if (cells[i]) setAt(i, '');
    } else if (k === 'ArrowLeft') {
      e.preventDefault();
      focusAt(i - 1);
    } else if (k === 'ArrowRight') {
      e.preventDefault();
      focusAt(i + 1);
    } else if (k === 'Home') {
      e.preventDefault();
      focusAt(0);
    } else if (k === 'End') {
      e.preventDefault();
      focusAt(LENGTH - 1);
    } else if (k.length === 1) {
      e.preventDefault(); // буквы, пробел, дефис — не цифры
    }
  };

  // Ввод без keydown с цифрой: экранные клавиатуры (key «Unidentified»), автозаполнение.
  const onInput = (i: number, raw: string) => {
    const digits = digitsOf(raw);
    if (!raw) return setAt(i, '');
    if (!digits) return; // одни не-цифры: ячейка как была
    if (digits.length === 1) {
      setAt(i, digits);
      if (i < LENGTH - 1) focusAt(i + 1);
      return;
    }
    // В ячейке уже была цифра и добавилась ещё одна — берём новую.
    if (digits.length === 2 && cells[i] && digits.includes(cells[i]!)) {
      const fresh = digits[0] === cells[i] ? digits[1]! : digits[0]!;
      setAt(i, fresh);
      if (i < LENGTH - 1) focusAt(i + 1);
      return;
    }
    fill(i, digits);
  };

  const onPaste = (i: number, e: ClipboardEvent<HTMLInputElement>) => {
    e.preventDefault();
    fill(i, e.clipboardData.getData('text'));
  };

  return (
    <div
      role="group"
      aria-label={label}
      aria-describedby={describedBy}
      className={cx(styles.group, invalid && styles.invalid)}
    >
      {cells.map((c, i) => (
        <input
          key={i}
          id={`${id}-${i}`}
          ref={(el) => {
            inputs.current[i] = el;
          }}
          className={cx(styles.cell, c && styles.filled)}
          type="text"
          inputMode="numeric"
          pattern="[0-9]*"
          autoComplete="one-time-code"
          autoFocus={autoFocus && i === 0}
          aria-label={t('digit', { index: i + 1, count: LENGTH })}
          aria-invalid={invalid || undefined}
          disabled={disabled}
          value={c}
          onKeyDown={(e) => onKeyDown(i, e)}
          onChange={(e) => onInput(i, e.target.value)}
          onPaste={(e) => onPaste(i, e)}
          onFocus={(e) => e.target.select()}
        />
      ))}
    </div>
  );
}
