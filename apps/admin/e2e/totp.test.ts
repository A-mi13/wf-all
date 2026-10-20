// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { base32Decode, hotp, totp, TotpDevice, totpStep } from './totp';

// Векторы RFC 4226 (приложение D) и RFC 6238 (приложение B, SHA-1): секрет — ASCII «12345678901234567890».
const RFC_SECRET = Buffer.from('12345678901234567890', 'ascii');

describe('hotp — RFC 4226', () => {
  const vectors = [
    '755224',
    '287082',
    '359152',
    '969429',
    '338314',
    '254676',
    '287922',
    '162583',
    '399871',
    '520489',
  ];
  it.each(vectors.map((code, counter) => [counter, code]))('счётчик %i → %s', (counter, code) => {
    expect(hotp(RFC_SECRET, counter)).toBe(code);
  });
});

describe('totp — RFC 6238 (SHA-1, 8 цифр)', () => {
  it.each([
    [59, '94287082'],
    [1111111109, '07081804'],
    [1111111111, '14050471'],
    [1234567890, '89005924'],
    [2000000000, '69279037'],
    [20000000000, '65353130'],
  ])('T = %i → %s', (t, code) => {
    expect(totp(RFC_SECRET, t, 8)).toBe(code);
  });

  it('по умолчанию 6 цифр (младшие 6 из RFC)', () => {
    expect(totp(RFC_SECRET, 59)).toBe('287082');
  });

  it('шаг 30 секунд: границы шага', () => {
    expect(totpStep(0)).toBe(0);
    expect(totpStep(29.999)).toBe(0);
    expect(totpStep(30)).toBe(1);
    expect(totpStep(1111111109)).toBe(37037036);
  });
});

describe('base32Decode — RFC 4648', () => {
  it('секрет RFC в base32', () => {
    expect(base32Decode('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ')).toEqual(RFC_SECRET);
  });

  it('строчные, пробелы и «=» не мешают', () => {
    expect(base32Decode('gezd gnbv gy3t qojq gezd gnbv gy3t qojq')).toEqual(RFC_SECRET);
    expect(base32Decode('MZXW6===')).toEqual(Buffer.from('foo'));
  });

  it('чужой символ — ошибка, а не тихий мусор', () => {
    expect(() => base32Decode('GEZD1')).toThrow(/base32/);
  });
});

describe('TotpDevice — код каждого шага один раз', () => {
  // Часы и сон — подставные: сон двигает часы, ожидание видно по суммарному сну.
  function device(startMs: number) {
    let now = startMs;
    const slept: number[] = [];
    const d = new TotpDevice(RFC_SECRET, {
      now: () => now,
      sleep: async (ms) => {
        slept.push(ms);
        now += ms;
      },
    });
    return { d, slept, at: () => now };
  }

  it('первый код — текущего шага, без ожидания', async () => {
    const { d, slept } = device(59_000);
    expect(await d.next()).toBe(totp(RFC_SECRET, 59));
    expect(slept).toEqual([]);
  });

  it('второй код в том же шаге — ждёт начала следующего шага', async () => {
    const { d, slept, at } = device(31_000);
    const first = await d.next();
    const second = await d.next();
    expect(first).toBe(totp(RFC_SECRET, 31));
    expect(second).toBe(totp(RFC_SECRET, 60));
    expect(slept.reduce((a, b) => a + b, 0)).toBeGreaterThanOrEqual(29_000);
    expect(totpStep(at() / 1000)).toBe(2);
  });

  it('шаг уже сменился — следующий код без ожидания', async () => {
    const { d, slept } = device(10_000);
    await d.next();
    const later = new TotpDevice(RFC_SECRET, { now: () => 45_000, sleep: async () => {} });
    expect(await later.next()).toBe(totp(RFC_SECRET, 45));
    expect(slept).toEqual([]);
  });

  it('last — код последнего выданного шага (для проверки отказа повтора)', async () => {
    const { d } = device(59_000);
    expect(d.last()).toBeUndefined();
    const code = await d.next();
    expect(d.last()).toBe(code);
  });
});
