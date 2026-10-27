import { createHmac } from 'node:crypto';

// TOTP для живого e2e (R12): своя реализация RFC 6238 поверх HOTP RFC 4226 — SHA-1, шаг 30 с, 6 цифр,
// как у admin-api (спека сотрудников §4.5). Без зависимостей: тест считает коды так же, как телефон
// сотрудника, а не той же библиотекой, что сервер.

const STEP_SECONDS = 30;
const BASE32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

// HOTP (RFC 4226 §5.3): HMAC-SHA-1 от счётчика (8 байт big-endian), динамическое усечение.
export function hotp(key: Buffer, counter: number, digits = 6): string {
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(counter));
  const mac = createHmac('sha1', key).update(msg).digest();
  const offset = mac[mac.length - 1]! & 0x0f;
  const bin = mac.readUInt32BE(offset) & 0x7fffffff;
  return String(bin % 10 ** digits).padStart(digits, '0');
}

// Номер шага времени (RFC 6238 §4.2, T0 = 0).
export function totpStep(unixSeconds: number): number {
  return Math.floor(unixSeconds / STEP_SECONDS);
}

export function totp(secret: Buffer, unixSeconds: number, digits = 6): string {
  return hotp(secret, totpStep(unixSeconds), digits);
}

// base32 RFC 4648: регистр, пробелы и «=» не важны (секрет на экране — группами по 4).
export function base32Decode(s: string): Buffer {
  const clean = s.replace(/[\s=]/g, '').toUpperCase();
  const out: number[] = [];
  let bits = 0;
  let acc = 0;
  for (const ch of clean) {
    const v = BASE32.indexOf(ch);
    if (v < 0) throw new Error(`base32: недопустимый символ «${ch}»`);
    acc = (acc << 5) | v;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out.push((acc >> bits) & 0xff);
    }
  }
  return Buffer.from(out);
}

type DeviceClock = { now: () => number; sleep: (ms: number) => Promise<void> };

const realClock: DeviceClock = {
  now: () => Date.now(),
  sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
};

// «Телефон» сотрудника: код каждого шага выдаёт один раз. Сервер помнит последний принятый шаг
// (last_used_step) и повтор отвергает, поэтому следующий код — только следующего шага: если текущий
// шаг уже выдан, next() ждёт его конца (до 30 с). Время — системное, как у admin-api на той же машине.
export class TotpDevice {
  private lastStep: number | undefined;
  private lastCode: string | undefined;

  constructor(
    private readonly secret: Buffer,
    private readonly clock: DeviceClock = realClock,
  ) {}

  async next(): Promise<string> {
    let step = totpStep(this.clock.now() / 1000);
    while (this.lastStep !== undefined && step <= this.lastStep) {
      // +50 мс за границей шага: таймер не срабатывает раньше срока, но и не впритык
      const wait = (this.lastStep + 1) * STEP_SECONDS * 1000 - this.clock.now() + 50;
      await this.clock.sleep(Math.max(wait, 1));
      step = totpStep(this.clock.now() / 1000);
    }
    this.lastStep = step;
    this.lastCode = hotp(this.secret, step);
    return this.lastCode;
  }

  // Последний выданный код (уже принятый сервером) — для проверки, что повтор отвергается.
  last(): string | undefined {
    return this.lastCode;
  }
}
