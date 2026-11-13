import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import type { ChipTone } from '../../ui';

// Статус города для чипа (макет 07, «Города и запуск»): живой — успех, пилот — внимание, ожидание —
// справочное. Список в контракте открытый (x-extensible-enum): незнакомый статус — нейтральный «—»,
// не сам код (сотрудник его не поймёт).
const TONES: Record<string, ChipTone> = { live: 'success', pilot: 'warning', waitlist: 'neutral' };

export function cityStatusTone(status: string): ChipTone {
  // hasOwn: «constructor» и прочие имена прототипа — не статусы
  return (Object.hasOwn(TONES, status) && TONES[status]) || 'neutral';
}

export function useCityStatus(): (status: string) => { tone: ChipTone; text: string } {
  const t = useTranslations('geo.status');
  return useCallback(
    (status) => ({
      tone: cityStatusTone(status),
      text: Object.hasOwn(TONES, status) ? t(status) : t('unknown'),
    }),
    [t],
  );
}
