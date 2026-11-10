import type { Story, StoryMeta } from '../showcase/csf';
import styles from '../showcase/Stories.module.css';
import { Avatar } from './Avatar';

// Аватар — инициалы, декоративный: два размера, одно слово, длинное имя, латиница.
export default { title: 'UI/Avatar' } satisfies StoryMeta;

// имена — данные сотрудников (как в фикстурах тестов), не тексты интерфейса
const NAMES = ['Ольга Владимирова', 'Иван', 'Maria Fernanda Gonzalez Rodriguez'];

export const Default: Story = () => (
  <div className={styles.row}>
    {NAMES.map((name) => (
      <Avatar key={name} name={name} />
    ))}
    {NAMES.map((name) => (
      <Avatar key={`md-${name}`} name={name} size="md" />
    ))}
  </div>
);
