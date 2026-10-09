import { writable } from 'vela/store';

const DEMO_USERS = [
  { id: 1, username: 'admin', password: 'admin', name: 'Alex Johnson', role: 'admin', avatar: '👨‍💻' },
  { id: 2, username: 'demo',  password: 'demo',  name: 'Sam Rivera',  role: 'viewer', avatar: '👩‍🎨' },
];

export const auth = writable({ user: null, isAuthenticated: false });

export function login(username, password) {
  return new Promise((resolve, reject) => {
    setTimeout(() => {
      const user = DEMO_USERS.find(u => u.username === username && u.password === password);
      if (user) {
        const { password: _, ...safe } = user;
        auth.set({ user: safe, isAuthenticated: true });
        resolve(safe);
      } else {
        reject(new Error('Invalid credentials'));
      }
    }, 600);
  });
}

export function logout() {
  auth.set({ user: null, isAuthenticated: false });
}
