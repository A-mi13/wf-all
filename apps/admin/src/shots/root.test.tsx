import { render, screen } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { useTranslations } from 'use-intl';
import { createApi } from '../api/client';
import { createQueryClient } from '../app/query';
import { meGeoModerator } from '../test/geo';
import { SHOWCASE_NOW } from '../showcase/params';
import { meAdmin } from '../test/me';
import { http, server, TEST_API } from '../test/server';
import { useNow } from '../ui/time';
import { shotsMe } from './params';
import { ShotsRoot } from './ShotsRoot';

// Режим shots (план UF1a, раунд 1 ревью: K-I1, m1, m4): параметры адреса → me фикстуры; история
// компонента — внутри тех же провайдеров, что приложение.

describe('shotsMe: ?shots-role и ?shots-locale', () => {
  const me = (q: string) => shotsMe(new URLSearchParams(q));

  test('по умолчанию — админ; роль и язык из адреса', () => {
    expect(me('')).toEqual({ role: 'admin', me: meAdmin });
    expect(me('shots-role=moderator&shots-locale=en')).toEqual({
      role: 'moderator',
      me: { ...meGeoModerator, locale: 'en' },
    });
    expect(me('shots-role=anonymous&shots-locale=en')).toEqual({ role: 'anonymous', me: null });
  });

  test('неизвестный язык — ошибка, а не молча язык по умолчанию', () => {
    expect(() => me('shots-locale=de')).toThrow(/неизвестный язык de/);
  });

  test('неизвестная роль и ключи прототипа — ошибка', () => {
    expect(() => me('shots-role=root')).toThrow(/неизвестная роль root/);
    expect(() => me('shots-role=toString')).toThrow(/неизвестная роль toString/);
    expect(() => me('shots-role=__proto__')).toThrow(/неизвестная роль __proto__/);
  });
});

describe('ShotsRoot', () => {
  function Translated() {
    const t = useTranslations();
    return <p>{t('nav.logout')}</p>;
  }

  test('история с useTranslations рендерится внутри провайдеров приложения', async () => {
    server.use(http.get('/v1/staff/me', ({ response }) => response(200).json(meAdmin)));
    render(
      <ShotsRoot api={createApi(TEST_API)} queryClient={createQueryClient()} Story={Translated} />,
    );
    expect(await screen.findByText('Выйти')).toBeInTheDocument();
  });

  // План UF2a.9 / бриф UF2a/III-S п. 1: «5 мин назад» в снимках не плывёт — «сейчас» зафиксирован SHOWCASE_NOW
  // и для экранов, и для историй.
  test('«сейчас» — SHOWCASE_NOW, а не часы машины', async () => {
    function Now() {
      return <p>{useNow('minute').toISOString()}</p>;
    }
    server.use(http.get('/v1/staff/me', ({ response }) => response(200).json(meAdmin)));
    render(<ShotsRoot api={createApi(TEST_API)} queryClient={createQueryClient()} Story={Now} />);
    expect(await screen.findByText(SHOWCASE_NOW.toISOString())).toBeInTheDocument();
  });
});
