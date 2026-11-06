export interface Format {
  upper: boolean;
}

export function apply(text: string, format: Format | null): string {
  return format?.upper ? text.toUpperCase() : text;
}
