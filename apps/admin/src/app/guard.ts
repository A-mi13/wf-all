import { startPath } from './nav';
import { AUTH_PATHS, paths } from './paths';
import type { SessionState } from './session';

// Куда пускает стадия сессии (спека §9.3, P19). Решение — чистая функция: роутер вызывает её в
// beforeLoad корневого маршрута на каждом переходе и при смене сессии.
// - без сессии — только вход и принятие приглашения; остальное → /login?return=<исходный адрес>
//   (после явного выхода — /login без return);
// - mfa_pending — только /login (шаг 2 входа), return сохраняется;
// - setup — только /setup-2fa (и после перезагрузки);
// - active — экраны входа и корень → ?return= или стартовая страница.
// Право на раздел проверяет сам маршрут (ForbiddenState вместо редиректа, D23).

export type Redirect = { to: string; search?: { return: string } } | { href: string };
export type Location = { pathname: string; href: string; search: Record<string, unknown> };

// Возврат — только путь этого приложения: не //host, не /\host, не схема, не экраны входа. Без
// управляющих символов: браузер выбрасывает из URL табуляцию и переводы строк, и «/\t/host»
// стал бы «//host».
const CONTROL = /[\x00-\x1f\x7f]/;

export function safeReturn(v: unknown): string | undefined {
  if (
    typeof v !== 'string' ||
    !v.startsWith('/') ||
    v.startsWith('//') ||
    v.startsWith('/\\') ||
    CONTROL.test(v)
  ) {
    return undefined;
  }
  const pathname = v.split(/[?#]/, 1)[0] ?? v;
  if (pathname === paths.root || AUTH_PATHS.includes(pathname)) return undefined;
  return v;
}

const toLogin = (loc: Location, ret?: string): Redirect => {
  const back = ret ?? safeReturn(loc.href);
  return back ? { to: paths.login, search: { return: back } } : { to: paths.login };
};

export function resolveRoute(session: SessionState, loc: Location): Redirect | null {
  const at = loc.pathname;
  switch (session.status) {
    case 'loading':
    case 'error':
      return null; // роутер в этих состояниях не показывается
    case 'anonymous':
      if (at === paths.login || at === paths.accept) return null;
      // после явного выхода — без возврата: адрес прежнего сотрудника следующему не нужен
      return session.signedOut ? { to: paths.login } : toLogin(loc);
    case 'mfa_pending':
      if (at === paths.login) return null;
      return toLogin(loc);
    case 'setup':
      return at === paths.setup2fa ? null : { to: paths.setup2fa };
    case 'active':
      if (at === paths.root || AUTH_PATHS.includes(at)) {
        const back = at === paths.login ? safeReturn(loc.search.return) : undefined;
        return back ? { href: back } : { to: startPath(session.me) };
      }
      return null;
  }
}
