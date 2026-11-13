import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import stories from '../../showcase/Stories.module.css';
import { Button } from '../Button';
import { DesktopOnlyNotice } from '../states';
import { ActionGate } from './ActionGate';

// «Только с компьютера» (спека UI-фундамента §3.2): ActionGate на текущем устройстве (на компьютере —
// действие, на телефоне с пальцем — пометка) и обе формы пометки — блок и одна строка с иконкой.
export default { title: 'UI/ActionGate', visual: true } satisfies StoryMeta;

export const OnThisDevice: Story = () => {
  const t = useTranslations('staff.drawer.disable');
  return (
    <ActionGate permission="staff.roles.revoke" compact>
      <Button variant="danger-outline">{t('button')}</Button>
    </ActionGate>
  );
};
OnThisDevice.storyName = 'On this device';

export const Notice: Story = () => (
  <div className={stories.column}>
    <DesktopOnlyNotice />
    <DesktopOnlyNotice compact />
  </div>
);
Notice.storyName = 'Notice block and line';
