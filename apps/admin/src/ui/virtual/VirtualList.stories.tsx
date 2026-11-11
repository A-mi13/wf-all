import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import stories from '../../showcase/Stories.module.css';
import samples from './VirtualSamples.module.css';
import { VirtualList } from './VirtualList';

// Длинный список (спека UI-фундамента §4.5): в DOM — видимые строки и запас, положение — числами
// --wf-virtual-*; окно прокрутки — высота родителя. Строки разной высоты меряются по факту.
export default { title: 'UI/VirtualList', visual: true, layout: true } satisfies StoryMeta;

// города — данные (как в фикстурах), не тексты интерфейса
const CITIES = Array.from({ length: 2000 }, (_, i) => `Город ${i + 1} · GeoNames ${524901 + i}`);

export const Long: Story = () => {
  const t = useTranslations('table');
  return (
    <div className={stories.frame}>
      <VirtualList
        label={t('shown', { count: CITIES.length })}
        items={CITIES}
        estimate={40}
        renderItem={(city) => <div className={samples.row}>{city}</div>}
      />
    </div>
  );
};
Long.storyName = 'Long list';

export const Varied: Story = () => {
  const t = useTranslations('table');
  return (
    <div className={stories.frame}>
      <VirtualList
        label={t('shown', { count: CITIES.length })}
        items={CITIES}
        estimate={40}
        renderItem={(city, i) => (
          <div className={samples.row}>
            {i % 3 === 0 ? `${city} — ${city} — ${city} — ${city} — ${city}` : city}
          </div>
        )}
      />
    </div>
  );
};
Varied.storyName = 'Varied heights';
