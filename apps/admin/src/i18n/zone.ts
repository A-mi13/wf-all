// Пояса и полночь (журнал: период → UTC). Здесь — потому что экраны (src/features) не вызывают Intl напрямую
// (lint-правило no-restricted-syntax, спека UI-фундамента §4.7); перенесено из features/audit/search.ts без
// изменения поведения (план UF2a.9, m8).

// Смещение пояса timeZone от UTC в момент ts, мс (местное − UTC).
function offsetAt(ts: number, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(ts);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  const local = Date.UTC(
    get('year'),
    get('month') - 1,
    get('day'),
    get('hour'),
    get('minute'),
    get('second'),
  );
  return local - Math.floor(ts / 1000) * 1000;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

// Местная полночь дня day (YYYY-MM-DD) в поясе timeZone — момент UTC (ISO). Смещение берётся на
// сам этот момент (переходы на летнее и зимнее время). Перевод часов вперёд ровно в полночь
// (Сантьяго, Гавана): полуночи нет — первый существующий момент дня (ревью M3), а не час прошлого дня.
export function dayStartUtc(day: string, timeZone: string, shiftDays = 0): string {
  const [y, m, d] = day.split('-').map(Number);
  const wall = Date.UTC(y ?? 0, (m ?? 1) - 1, (d ?? 1) + shiftDays);
  const local = (ts: number) => ts + offsetAt(ts, timeZone);
  let ts = wall - offsetAt(wall, timeZone);
  const again = offsetAt(ts, timeZone);
  if (again !== wall - ts) ts = wall - again;
  if (local(ts) !== wall) {
    // полуночи нет: двоичный поиск первой минуты, где местное время уже не раньше полуночи
    // (переводы часов — на границах минут; сдвиг не больше пары часов)
    let lo = ts - 3 * HOUR;
    let hi = ts + 3 * HOUR;
    while (hi - lo > MINUTE) {
      const mid = lo + Math.floor((hi - lo) / 2 / MINUTE) * MINUTE;
      if (local(mid) >= wall) hi = mid;
      else lo = mid;
    }
    ts = hi;
  }
  return new Date(ts).toISOString();
}
