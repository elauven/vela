const STATUS_LABELS = {
  paid: 'Оплачен',
  pending: 'Ожидает',
  canceled: 'Отменён',
};

const CUSTOMERS = [
  'Анна Смирнова',
  'Иван Петров',
  'ООО «Вектор»',
  'Мария Козлова',
  'ИП Николаев',
  'Сергей Волков',
  'ООО «Радуга»',
  'Елена Морозова',
];

function formatDate(date) {
  const day = String(date.getDate()).padStart(2, '0');
  const month = String(date.getMonth() + 1).padStart(2, '0');
  return `${day}.${month}`;
}

export function generateDays(days, seed) {
  const result = [];
  const today = new Date();
  for (let i = 0; i < days; i++) {
    const date = new Date(
      today.getFullYear(),
      today.getMonth(),
      today.getDate() - (days - 1 - i)
    );
    const wave = Math.sin((i + seed) * 0.7);
    const noise = ((i + 1) * 37 + seed * 13) % 23;
    const orders = Math.max(4, Math.round(18 + 8 * wave + noise / 2));
    const averageCheck = 2400 + 600 * Math.cos((i + seed) * 0.35);
    result.push({
      label: formatDate(date),
      revenue: Math.round(orders * averageCheck),
      orders,
      visits: orders * (9 + (noise % 4)),
    });
  }
  return result;
}

export function generateOrders(count, seed) {
  const statusNames = Object.keys(STATUS_LABELS);
  const orders = [];
  for (let i = 0; i < count; i++) {
    const status = statusNames[(i + seed) % statusNames.length];
    orders.push({
      id: seed * 100 + i,
      customer: CUSTOMERS[(i * 3 + seed) % CUSTOMERS.length],
      amount: 1200 + ((i * 997 + seed * 271) % 8800),
      status,
      statusLabel: STATUS_LABELS[status],
    });
  }
  return orders;
}
