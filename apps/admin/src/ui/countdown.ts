import { useCallback, useEffect, useState } from 'react';

// Отсчёт секунд до повтора: 429 с Retry-After (вход, приглашение), пауза 60 с повторной отправки
// кода. start(seconds) запускает отсчёт заново, даже с тем же числом (повторный 429, повторная
// отправка); left — сколько осталось, вверх до целой; 0 — можно. Срок считается от Date.now(), а
// не числом тиков: вкладка в фоне тикает реже, а отсчёт не врёт.
export function useCountdown(): { left: number; start: (seconds: number) => void } {
  const [run, setRun] = useState<{ deadline: number } | null>(null);
  const [left, setLeft] = useState(0);

  const start = useCallback((seconds: number) => {
    const total = Math.max(0, Math.ceil(seconds));
    setLeft(total);
    // новый объект — новый отсчёт и при том же сроке
    setRun(total > 0 ? { deadline: Date.now() + total * 1000 } : null);
  }, []);

  useEffect(() => {
    if (!run) return;
    const timer = setInterval(() => {
      const rest = Math.max(0, Math.ceil((run.deadline - Date.now()) / 1000));
      setLeft(rest);
      if (rest === 0) setRun(null);
    }, 1000);
    return () => clearInterval(timer);
  }, [run]);

  return { left, start };
}
