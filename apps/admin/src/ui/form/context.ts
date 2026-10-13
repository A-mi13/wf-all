import { createContext } from 'react';
import type { ApiError } from '../../api/client';

// Ошибка последней отправки формы, которую не разложить по полям (FormError без пропса).
export const FormErrorContext = createContext<ApiError | null>(null);
