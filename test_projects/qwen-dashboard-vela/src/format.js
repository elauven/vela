export function formatCurrency(value) {
  return new Intl.NumberFormat('ru-RU', {
    style: 'currency',
    currency: 'RUB',
    maximumFractionDigits: 0,
  }).format(value);
}

export function formatNumber(value) {
  return new Intl.NumberFormat('ru-RU').format(value);
}

export function formatPercent(value) {
  const sign = value > 0 ? '+' : '';
  return `${sign}${value.toFixed(1)}%`;
}

export function formatTime(date) {
  return new Intl.DateTimeFormat('ru-RU', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
}
