import { writable, derived } from 'vela/store';

// ── KPIs ────────────────────────────────────────────────────────────────────
export const kpis = writable([
  { id: 'revenue',  label: 'Revenue',       value: 84321, prev: 71200, unit: '$', icon: '💰', color: '#7c3aed' },
  { id: 'users',    label: 'Active Users',  value: 12453, prev: 11100, unit: '',  icon: '👥', color: '#2563eb' },
  { id: 'orders',   label: 'Orders',        value: 3218,  prev: 2980,  unit: '',  icon: '📦', color: '#059669' },
  { id: 'rate',     label: 'Conv. Rate',    value: 3.42,  prev: 3.10,  unit: '%', icon: '📈', color: '#dc2626' },
]);

// ── Orders ──────────────────────────────────────────────────────────────────
export const orders = writable([
  { id: 'ORD-001', customer: 'Alice Brown',    amount: 149.99, status: 'completed', date: '2024-01-15', items: 3 },
  { id: 'ORD-002', customer: 'Bob Martinez',   amount: 89.50,  status: 'pending',   date: '2024-01-16', items: 1 },
  { id: 'ORD-003', customer: 'Carol White',    amount: 320.00, status: 'completed', date: '2024-01-16', items: 5 },
  { id: 'ORD-004', customer: 'David Kim',      amount: 55.25,  status: 'cancelled', date: '2024-01-17', items: 2 },
  { id: 'ORD-005', customer: 'Eva Rossi',      amount: 210.75, status: 'shipped',   date: '2024-01-17', items: 4 },
  { id: 'ORD-006', customer: 'Frank Chen',     amount: 95.00,  status: 'pending',   date: '2024-01-18', items: 2 },
  { id: 'ORD-007', customer: 'Grace Patel',    amount: 430.50, status: 'completed', date: '2024-01-18', items: 7 },
  { id: 'ORD-008', customer: 'Henry Okafor',   amount: 67.80,  status: 'shipped',   date: '2024-01-19', items: 1 },
]);

// ── Users ───────────────────────────────────────────────────────────────────
export const users = writable([
  { id: 1, name: 'Alice Brown',   email: 'alice@example.com',  role: 'admin',  status: 'active',   joined: '2023-03-10', avatar: '🧑' },
  { id: 2, name: 'Bob Martinez',  email: 'bob@example.com',    role: 'editor', status: 'active',   joined: '2023-05-22', avatar: '👦' },
  { id: 3, name: 'Carol White',   email: 'carol@example.com',  role: 'viewer', status: 'inactive', joined: '2023-07-01', avatar: '👩' },
  { id: 4, name: 'David Kim',     email: 'david@example.com',  role: 'editor', status: 'active',   joined: '2023-08-15', avatar: '🧔' },
  { id: 5, name: 'Eva Rossi',     email: 'eva@example.com',    role: 'viewer', status: 'active',   joined: '2023-11-03', avatar: '👱' },
]);

// ── Notifications ────────────────────────────────────────────────────────────
export const notifications = writable([
  { id: 1, type: 'success', message: 'Order #ORD-003 completed',     time: '2 min ago',  read: false },
  { id: 2, type: 'warning', message: 'Server load above 80%',        time: '15 min ago', read: false },
  { id: 3, type: 'info',    message: 'New user Eva Rossi registered', time: '1 hr ago',   read: true  },
  { id: 4, type: 'error',   message: 'Payment gateway timeout',       time: '3 hr ago',   read: true  },
  { id: 5, type: 'success', message: 'Database backup completed',     time: '5 hr ago',   read: true  },
]);

export function markRead(id) {
  notifications.update(ns => ns.map(n => n.id === id ? { ...n, read: true } : n));
}

export function markAllRead() {
  notifications.update(ns => ns.map(n => ({ ...n, read: true })));
}

export function deleteOrder(id) {
  orders.update(os => os.filter(o => o.id !== id));
}

export function updateOrderStatus(id, status) {
  orders.update(os => os.map(o => o.id === id ? { ...o, status } : o));
}

// ── Chart data (months) ──────────────────────────────────────────────────────
export const chartData = writable({
  labels: ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'],
  revenue: [41000, 47000, 52000, 49000, 63000, 71000, 68000, 75000, 80000, 77000, 84000, 92000],
  orders:  [210,   240,   280,   260,   310,   350,   330,   380,   410,   390,   420,   460],
});

// ── Settings ─────────────────────────────────────────────────────────────────
export const settings = writable({
  theme:       'light',
  currency:    'USD',
  dateFormat:  'MM/DD/YYYY',
  autoRefresh: false,
  refreshRate: 30,
  compactMode: false,
  language:    'en',
});

export function updateSetting(key, value) {
  settings.update(s => ({ ...s, [key]: value }));
}
