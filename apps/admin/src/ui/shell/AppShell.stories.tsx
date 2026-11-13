import { useTranslations } from 'use-intl';
import type { Me } from '../../app/permissions';
import { NAV } from '../../app/nav';
import type { Story, StoryMeta } from '../../showcase/csf';
import { RouterFrame, SessionFrame } from '../../showcase/frames';
import { meModerator, meOwner } from '../../test/me';
import { EmptyState } from '../states';
import { AppShell } from './AppShell';
import { PageHeader } from './PageHeader';

// Каркас админки: меню по правам (NAV приложения), карточка сотрудника, счётчик «Подтверждений»; на
// узкой ширине — таббар и «Ещё» (layout: три ширины).
export default { title: 'UI/Shell/AppShell', visual: true, layout: true } satisfies StoryMeta;

function Frame(props: { me: Me; name: string; email: string; details: string }) {
  const t = useTranslations();
  return (
    <SessionFrame me={props.me}>
      <RouterFrame path="/audit">
        <AppShell
          groups={NAV}
          account={{ name: props.name, email: props.email, details: props.details }}
          counts={{ '/approvals': 3 }}
          onLogout={() => undefined}
        >
          <PageHeader title={t('nav.audit')} subtitle={t('audit.subtitle')} />
          <EmptyState />
        </AppShell>
      </RouterFrame>
    </SessionFrame>
  );
}

// имя, почта и города — данные сотрудника (как в фикстурах тестов)
export const Default: Story = () => {
  const t = useTranslations('roles');
  return <Frame me={meOwner} name="Ольга В." email="olga.v@wf.example" details={t('owner')} />;
};

export const LongText: Story = () => {
  const t = useTranslations('roles');
  return (
    <Frame
      me={meModerator}
      name="Ольга Владимировна Константинопольская"
      email="olga.vladimirovna.moderator@stavropol.wf.example"
      details={`${t('moderator')} · Ставрополь, Пятигорск, Кисловодск, Ессентуки`}
    />
  );
};
