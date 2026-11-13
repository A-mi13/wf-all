import { ICONS, type IconName, iconGroup } from '@wf/icons';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Icon } from './Icon';
import { Illustration } from './Illustration';
import styles from './IconSheet.module.css';

// Иконки @wf/icons в админке (план UF1a.3): лист всех иконок на 16/20/24 по группам (артборд, меню,
// дорисованные *) и иллюстрации на 48/64 — вырезы и детали должны различаться в обеих темах.
export default { title: 'UI/Icon', visual: true } satisfies StoryMeta;

const NAMES = Object.keys(ICONS) as IconName[];
// группы — ключами пакета (артборд 14, меню из макета 08, дорисованные *): не тексты интерфейса
const GROUPS = ['artboard', 'nav', 'drawn'] as const;

export const AllIcons: Story = () => (
  <div className={styles.sheet}>
    {GROUPS.map((group) => [
      <h2 key={group} className={styles.group}>
        {group}
      </h2>,
      ...NAMES.filter((n) => iconGroup(n) === group).map((name) => (
        <div key={name} className={styles.cell}>
          <div className={styles.sizes}>
            <Icon name={name} size={16} />
            <Icon name={name} size={20} />
            <Icon name={name} size={24} />
          </div>
          <span className={styles.name}>{name}</span>
        </div>
      )),
    ])}
  </div>
);
AllIcons.storyName = 'all icons';

export const Illustrations: Story = () => (
  <div className={styles.sheet}>
    {NAMES.filter((n) => iconGroup(n) !== 'nav').map((name) => (
      <div key={name} className={styles.cell}>
        <div className={styles.sizes}>
          <Illustration name={name} size={48} />
          <Illustration name={name} size={64} />
        </div>
        <span className={styles.name}>{name}</span>
      </div>
    ))}
  </div>
);
Illustrations.storyName = 'illustrations';

export const WithLabel: Story = () => (
  <div className={styles.sheet}>
    <div className={styles.cell}>
      <Icon name="search" size={24} label="search" />
      <span className={styles.name}>role=img</span>
    </div>
  </div>
);
WithLabel.storyName = 'with label';
