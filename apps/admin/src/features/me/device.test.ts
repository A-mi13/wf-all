import { expect, test } from 'vitest';
import { deviceOf } from './device';

// Устройство сеанса из User-Agent (контракт: «устройство и браузер разбирает админка»).
test.each([
  [
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36',
    { browser: 'chrome', os: 'windows' },
  ],
  [
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36 Edg/141.0',
    { browser: 'edge', os: 'windows' },
  ],
  [
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
    { browser: 'safari', os: 'macos' },
  ],
  [
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
    { browser: 'safari', os: 'ios' },
  ],
  [
    'Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0',
    { browser: 'firefox', os: 'linux' },
  ],
  [
    'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Mobile Safari/537.36 OPR/80.0',
    { browser: 'opera', os: 'android' },
  ],
  [
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0 YaBrowser/25.8.0 Safari/537.36',
    { browser: 'yandex', os: 'windows' },
  ],
  ['curl/8.0', { browser: 'other', os: 'other' }],
])('%s', (ua, want) => {
  expect(deviceOf(ua)).toEqual(want);
});

test('браузер не прислал User-Agent — устройство не определено', () => {
  expect(deviceOf(null)).toBeNull();
  expect(deviceOf('')).toBeNull();
});
