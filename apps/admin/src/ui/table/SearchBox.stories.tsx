import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { SearchBox } from './DataTable';

// Поиск страницы без таблицы строк (матрица флагов, Task 22): то же поле, что в панели DataTable, — набор уходит
// после паузы, «×» очищает, клавиша «/» каркаса ставит в него фокус.
export default { title: 'UI/SearchBox', visual: true } satisfies StoryMeta;

function Sample({ initial }: { initial: string }) {
  const t = useTranslations('table');
  const [value, setValue] = useState(initial);
  return (
    <div className={styles.column}>
      <SearchBox value={value} onChange={setValue} placeholder={t('search')} />
    </div>
  );
}

export const Empty: Story = () => <Sample initial="" />;
Empty.storyName = 'Empty';

export const Filled: Story = () => <Sample initial="Став" />;
Filled.storyName = 'Filled';
