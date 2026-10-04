const BASE_URL = 'https://jsonplaceholder.typicode.com';

async function request(path, { signal } = {}) {
  let response;
  try {
    response = await fetch(`${BASE_URL}${path}`, { signal });
  } catch (error) {
    if (error.name === 'AbortError') throw error;
    throw new Error('Сеть недоступна. Проверьте соединение.');
  }
  if (!response.ok) {
    throw new Error(`Сервер вернул ошибку ${response.status}.`);
  }
  return response.json();
}

export function fetchOverview({ signal } = {}) {
  return Promise.all([
    request('/users', { signal }),
    request('/posts', { signal }),
    request('/todos', { signal }),
  ]).then(([users, posts, todos]) => ({
    users: users.length,
    posts: posts.length,
    todosTotal: todos.length,
    todosCompleted: todos.filter((todo) => todo.completed).length,
  }));
}

export function fetchPosts(limit, { signal } = {}) {
  const suffix = limit ? `?_limit=${limit}` : '';
  return request(`/posts${suffix}`, { signal });
}

export function fetchUsers({ signal } = {}) {
  return request('/users', { signal });
}

export function fetchUser(id, { signal } = {}) {
  return request(`/users/${id}`, { signal });
}

export function fetchUserTodos(id, { signal } = {}) {
  return request(`/users/${id}/todos`, { signal });
}
