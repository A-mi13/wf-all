// Устройство сеанса из строки User-Agent (контракт StaffSession.user_agent: «устройство и браузер
// разбирает админка»). Возвращает ключи локалей me.sessions.browsers.* и me.sessions.systems.*;
// null — браузер строку не прислал. Порядок проверок важен: Edge, Opera и Яндекс Браузер пишут в
// строку и «Chrome», Chrome — «Safari», iOS — «Mac OS X».
export type Device = {
  browser: 'chrome' | 'edge' | 'firefox' | 'safari' | 'opera' | 'yandex' | 'other';
  os: 'windows' | 'macos' | 'ios' | 'android' | 'linux' | 'other';
};

export function deviceOf(ua: string | null): Device | null {
  if (!ua) return null;
  return { browser: browserOf(ua), os: osOf(ua) };
}

function browserOf(ua: string): Device['browser'] {
  if (/\bEdg(e|A|iOS)?\//.test(ua)) return 'edge';
  if (/\b(OPR|Opera)\//.test(ua)) return 'opera';
  if (/\bYaBrowser\//.test(ua)) return 'yandex';
  if (/\b(Firefox|FxiOS)\//.test(ua)) return 'firefox';
  if (/\b(Chrome|CriOS|Chromium)\//.test(ua)) return 'chrome';
  if (/\bSafari\//.test(ua) && /\bVersion\//.test(ua)) return 'safari';
  return 'other';
}

function osOf(ua: string): Device['os'] {
  if (/\b(iPhone|iPad|iPod)\b/.test(ua)) return 'ios';
  if (/\bAndroid\b/.test(ua)) return 'android';
  if (/\bWindows\b/.test(ua)) return 'windows';
  if (/\bMac OS X\b|\bMacintosh\b/.test(ua)) return 'macos';
  if (/\bLinux\b|\bX11\b/.test(ua)) return 'linux';
  return 'other';
}
