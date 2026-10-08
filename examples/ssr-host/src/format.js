// Runs in the browser. Intl cannot be translated to Go, so the server uses
// the host implementation in ssrhost/src/format/format_price_host.go.
export function formatPrice(value, currency) {
  return new Intl.NumberFormat('en-US', { style: 'currency', currency }).format(value);
}
