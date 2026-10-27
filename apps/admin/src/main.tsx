import '@wf/tokens/tokens.css';
import './app/fonts';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createApi } from './api/client';
import { createQueryClient } from './app/query';

const root = document.getElementById('root');
if (!root) throw new Error('нет #root в index.html');
createRoot(root).render(
  <StrictMode>
    {/* Тот же origin (§5.2): относительные /v1/* — dev-прокси Vite или сам admin-api */}
    <App api={createApi('')} queryClient={createQueryClient()} />
  </StrictMode>,
);
