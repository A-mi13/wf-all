import { MAILPIT_URL } from './env';

// Письма живого e2e — API Mailpit (1.31: GET /api/v1/messages, GET /api/v1/message/{ID}). Письма
// сотрудникам шлёт фон admin-api (River, очередь staff): не сразу после ответа — ждём с таймаутом.

type Summary = { ID: string; To: { Address: string }[]; Subject: string; Created: string };
export type Mail = { id: string; subject: string; text: string };

async function json<T>(url: string): Promise<T> {
  const r = await fetch(url);
  if (!r.ok) throw new Error(`Mailpit ${url}: ${r.status}`);
  return (await r.json()) as T;
}

// Ждёт письмо получателю, подходящее под match (по тексту), новее уже виденных (seen). Возвращает
// самое новое подходящее.
export async function waitForMail(
  to: string,
  opts: { match?: RegExp; seen?: ReadonlySet<string>; timeoutMs?: number } = {},
): Promise<Mail> {
  const deadline = Date.now() + (opts.timeoutMs ?? 30_000);
  const want = to.toLowerCase();
  for (;;) {
    const list = await json<{ messages: Summary[] }>(`${MAILPIT_URL}/api/v1/messages?limit=200`);
    // Mailpit отдаёт новые первыми
    for (const m of list.messages) {
      if (opts.seen?.has(m.ID)) continue;
      if (!m.To.some((a) => a.Address.toLowerCase() === want)) continue;
      const full = await json<{ Text: string; Subject: string }>(
        `${MAILPIT_URL}/api/v1/message/${m.ID}`,
      );
      if (opts.match && !opts.match.test(full.Text)) continue;
      return { id: m.ID, subject: full.Subject, text: full.Text };
    }
    if (Date.now() > deadline)
      throw new Error(`e2e: письма для ${to} нет за ${opts.timeoutMs ?? 30_000} мс`);
    await new Promise((r) => setTimeout(r, 250));
  }
}

// Код приглашения в письме: 10 символов base32 строчными, «xxxxx-xxxxx» (спека §4.3).
export const INVITE_CODE = /\b([a-z2-7]{5}-[a-z2-7]{5})\b/;

export function inviteCode(text: string): string {
  const m = INVITE_CODE.exec(text);
  if (!m?.[1]) throw new Error('e2e: в письме нет кода приглашения');
  return m[1];
}

// Письма получателю: id и момент приёма Mailpit (мс), новые первыми.
export async function mailList(to: string): Promise<{ id: string; created: number }[]> {
  const list = await json<{ messages: Summary[] }>(`${MAILPIT_URL}/api/v1/messages?limit=200`);
  const want = to.toLowerCase();
  return list.messages
    .filter((m) => m.To.some((a) => a.Address.toLowerCase() === want))
    .map((m) => ({ id: m.ID, created: Date.parse(m.Created) }));
}

// Все письма получателю (id) — чтобы потом ждать только новые.
export async function mailIds(to: string): Promise<Set<string>> {
  return new Set((await mailList(to)).map((m) => m.id));
}

// Писем получателю ровно n всё окно ms (опрос): фон admin-api шлёт не сразу, и одна проверка «сейчас
// одно» не доказывает, что второго не будет (повторное ревью 27.10, Minor 3).
export async function expectMailCountStable(to: string, n: number, ms = 3_000): Promise<void> {
  const deadline = Date.now() + ms;
  for (;;) {
    const got = (await mailList(to)).length;
    if (got !== n) throw new Error(`e2e: писем для ${to} — ${got}, ждали ровно ${n}`);
    if (Date.now() > deadline) return;
    await new Promise((r) => setTimeout(r, 250));
  }
}
