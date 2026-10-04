// Сохранение и загрузка страницы в localStorage.
const KEY = 'vela-builder:page:v1';

export function savePage(page) {
  try {
    localStorage.setItem(KEY, JSON.stringify({
      version: 1,
      title: page.title,
      blocks: page.blocks
    }));
  } catch { /* переполнение квоты и т.п. — молча пропускаем */ }
}

export function loadPage() {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    const data = JSON.parse(raw);
    if (!data || !Array.isArray(data.blocks)) return null;
    return data;
  } catch {
    return null;
  }
}

export function clearSaved() {
  localStorage.removeItem(KEY);
}
