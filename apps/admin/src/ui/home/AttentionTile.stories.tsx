import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { AttentionTile, AttentionTiles } from './AttentionTile';

// Плитки главной (спека UI-фундамента §4.13): число, «99+», ноль, примечание, загрузка, ошибка с «Повторить».
export default { title: 'UI/AttentionTile', visual: true } satisfies StoryMeta;

const noop = () => undefined;
const link = { href: '#tile', onNavigate: noop, onRetry: noop };

export const Ready: Story = () => {
  const t = useTranslations('home');
  return (
    <AttentionTiles label={t('tiles_label')}>
      <li>
        <AttentionTile
          {...link}
          title={t('incoming.title')}
          value={3}
          tone="warning"
          state="ready"
          note={t('incoming.desktop')}
        />
      </li>
      <li>
        <AttentionTile {...link} title={t('mine.title')} value={100} tone="accent" state="ready" />
      </li>
      <li>
        <AttentionTile
          {...link}
          title={t('imports.title')}
          value={1}
          tone="danger"
          state="ready"
          note={t('imports.scope')}
        />
      </li>
      <li>
        <AttentionTile
          {...link}
          title={t('waitlist.title')}
          value={0}
          tone="neutral"
          state="ready"
        />
      </li>
    </AttentionTiles>
  );
};

export const LoadingAndError: Story = () => {
  const t = useTranslations('home');
  return (
    <AttentionTiles label={t('tiles_label')}>
      <li>
        <AttentionTile
          {...link}
          title={t('incoming.title')}
          value={null}
          tone="warning"
          state="loading"
        />
      </li>
      <li>
        <AttentionTile {...link} title={t('mine.title')} value={null} tone="accent" state="error" />
      </li>
      <li>
        <AttentionTile
          {...link}
          title={t('imports.title')}
          value={null}
          tone="danger"
          state="error"
          retrying
        />
      </li>
    </AttentionTiles>
  );
};
