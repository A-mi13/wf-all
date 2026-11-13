import { useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { Popover, type PopoverProps } from './Popover';
import samples from './PopoverSamples.module.css';

// Основа всплывающего (спека UI-фундамента §4.8): открыт под якорем; у правого края окна —
// выравнивание по концу якоря; две подсказки разом — каждая от своего якоря (страж положения N3).
export default { title: 'UI/Popover', visual: true } satisfies StoryMeta;

function Sample({
  label,
  kind = 'menu',
  text,
}: {
  label: string;
  kind?: PopoverProps['kind'];
  text: string;
}) {
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(true);
  return (
    <>
      <Button ref={anchor} variant="secondary" onClick={() => setOpen((v) => !v)}>
        {label}
      </Button>
      <Popover anchor={anchor} open={open} kind={kind} onClose={() => setOpen(false)}>
        <p className={samples.content}>{text}</p>
      </Popover>
    </>
  );
}

export const Open: Story = () => {
  const t = useTranslations();
  const roles = (['owner', 'admin', 'moderator', 'support'] as const).map((r) => t(`roles.${r}`));
  return <Sample label={t('staff.drawer.role_change.role')} text={roles.join(' · ')} />;
};
Open.storyName = 'Open';

export const AtEdge: Story = () => {
  const t = useTranslations('nav');
  return (
    <div className={samples.edge}>
      <Sample label={t('more')} text={t('more_title')} />
    </div>
  );
};
AtEdge.storyName = 'At window edge';

export const TwoHints: Story = () => {
  const t = useTranslations();
  return (
    <div className={samples.rows}>
      <Sample kind="hint" label={t('nav.audit')} text={t('staff.two_hands.text')} />
      <Button variant="secondary">{t('nav.staff')}</Button>
      <Sample kind="hint" label={t('nav.approvals')} text={t('states.desktop_only.hint')} />
    </div>
  );
};
TwoHints.storyName = 'Two hints at once';
