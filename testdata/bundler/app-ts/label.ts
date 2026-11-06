import { shout } from 'tiny-fmt';

export type Label = 'count' | 'total';

export function label(name: Label): string {
  return shout(name) as string;
}
