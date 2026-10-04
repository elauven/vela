// ==================== Утилиты ====================

/**
 * Преобразует строку пути с динамическими сегментами в регулярное выражение
 * и возвращает массив имён параметров.
 */
function pathToRegex(path) {
  const paramNames = [];
  const pattern = path
    .replace(/:([^/]+)/g, (_, name) => {
      paramNames.push(name);
      return '([^/]+)';
    })
    .replace(/\*/g, '.*'); // поддержка wildcard
  return { regex: new RegExp(`^${pattern}$`), paramNames };
}

/**
 * Извлекает параметры из строки запроса (query string).
 */
function parseQuery(query) {
  if (!query) return {};
  return query
    .replace(/^\?/, '')
    .split('&')
    .reduce((acc, pair) => {
      const [key, value] = pair.split('=');
      acc[decodeURIComponent(key)] = decodeURIComponent(value || '');
      return acc;
    }, {});
}

/**
 * Строит полный путь с параметрами запроса.
 */
function buildPath(path, query = {}) {
  const qs = Object.keys(query)
    .filter(k => query[k] !== undefined && query[k] !== null)
    .map(k => `${encodeURIComponent(k)}=${encodeURIComponent(query[k])}`)
    .join('&');
  return qs ? `${path}?${qs}` : path;
}

// ==================== Основной класс ====================

class Router {
  constructor(routes, options = {}) {
    this.routes = routes.map(route => ({
      ...route,
      ...pathToRegex(route.path),
    }));
    this.mode = options.mode || 'history';
    this.currentRoute = null;
    this.listeners = [];
    this.isPopstate = false;

    // Обработчик события popstate
    this._onPopState = this._onPopState.bind(this);
    window.addEventListener('popstate', this._onPopState);

    // Инициализируем текущий маршрут
    this._resolve(window.location.pathname + window.location.search);
  }

  /**
   * Разрешает путь: находит подходящий маршрут, извлекает параметры,
   * обновляет currentRoute и уведомляет подписчиков.
   */
  _resolve(url) {
    const [pathname, queryString] = url.split('?');
    const query = parseQuery(queryString);
    let matched = null;
    let params = {};

    for (const route of this.routes) {
      const match = pathname.match(route.regex);
      if (match) {
        params = route.paramNames.reduce((acc, name, i) => {
          acc[name] = match[i + 1];
          return acc;
        }, {});
        matched = route;
        break;
      }
    }

    const newRoute = {
      path: pathname,
      params,
      query,
      component: matched ? matched.component : null,
      fullPath: url,
    };

    // Если маршрут не изменился, не триггерим обновления (кроме случая popstate)
    if (this.currentRoute && this.currentRoute.fullPath === url) {
      return;
    }

    this.currentRoute = newRoute;
    this._notify();
  }

  /**
   * Уведомляет всех подписчиков о новом маршруте.
   */
  _notify() {
    for (const listener of this.listeners) {
      try {
        listener(this.currentRoute);
      } catch (e) {
        console.error('Router listener error:', e);
      }
    }
  }

  /**
   * Обработчик popstate (нажатие кнопок назад/вперёд).
   */
  _onPopState(event) {
    const url = window.location.pathname + window.location.search;
    this.isPopstate = true;
    this._resolve(url);
    this.isPopstate = false;
  }

  /**
   * Переход на новый URL с добавлением записи в историю.
   */
  push(path, query) {
    const fullPath = buildPath(path, query);
    if (fullPath === this.currentRoute?.fullPath) return;
    window.history.pushState(null, '', fullPath);
    this._resolve(fullPath);
  }

  /**
   * Замена текущей записи в истории.
   */
  replace(path, query) {
    const fullPath = buildPath(path, query);
    window.history.replaceState(null, '', fullPath);
    this._resolve(fullPath);
  }

  /**
   * Навигация назад.
   */
  back() {
    window.history.back();
  }

  /**
   * Навигация вперёд.
   */
  forward() {
    window.history.forward();
  }

  /**
   * Перемещение по истории на delta шагов.
   */
  go(delta) {
    window.history.go(delta);
  }

  /**
   * Подписка на изменения маршрута.
   * Возвращает функцию отписки.
   */
  onChange(callback) {
    this.listeners.push(callback);
    // Если уже есть текущий маршрут, сразу вызываем колбэк
    if (this.currentRoute) {
      callback(this.currentRoute);
    }
    return () => {
      const idx = this.listeners.indexOf(callback);
      if (idx !== -1) this.listeners.splice(idx, 1);
    };
  }

  /**
   * Возвращает текущий объект маршрута.
   */
  getCurrent() {
    return this.currentRoute;
  }

  /**
   * Очистка (удаление слушателя popstate).
   */
  destroy() {
    window.removeEventListener('popstate', this._onPopState);
    this.listeners = [];
  }
}

// ==================== Фабрика ====================

export function createRouter(routes, options = {}) {
  return new Router(routes, options);
}

// ==================== Пример использования ====================

/*
import { createRouter } from './router.js';

const routes = [
  { path: '/', component: () => '<h1>Home</h1>' },
  { path: '/about', component: () => '<h1>About</h1>' },
  { path: '/user/:id', component: (params) => `<h1>User ${params.id}</h1>` },
  { path: '/docs/*', component: () => '<h1>Docs (catch-all)</h1>' },
];

const router = createRouter(routes);

// Подписка на изменения
router.onChange((route) => {
  console.log('Route changed:', route);
  // Здесь вы можете рендерить компонент в вашем фреймворке
  document.getElementById('app').innerHTML = route.component
    ? route.component(route.params)
    : '<h1>404 Not Found</h1>';
});

// Программная навигация
document.querySelector('#link-home')?.addEventListener('click', (e) => {
  e.preventDefault();
  router.push('/');
});

document.querySelector('#link-user')?.addEventListener('click', (e) => {
  e.preventDefault();
  router.push('/user/123', { sort: 'asc' });
});

// Доступ к текущему маршруту
console.log(router.getCurrent());
*/