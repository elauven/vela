import { createRouter } from './simple-router.js';

// Vela не поддерживает <svelte:component>, поэтому в поле `component`
// храним строковый ключ страницы: по нему App выбирает страницу в шаблоне.
const routes = [
  { path: '/', component: 'overview' },
  { path: '/users', component: 'users' },
  { path: '/users/:id', component: 'user-detail' },
  { path: '/settings', component: 'settings' },
];

export const router = createRouter(routes);

export function navigate(path, query) {
  router.push(path, query);
}
